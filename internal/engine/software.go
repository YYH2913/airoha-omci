// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"hash"
	"strings"

	omci "github.com/opencord/omci-lib-go/v2"
	me "github.com/opencord/omci-lib-go/v2/generated"
	"github.com/xg2010g/airoha-omci/internal/checksum"
	"github.com/xg2010g/airoha-omci/internal/mib"
	"github.com/xg2010g/airoha-omci/internal/software"
)

type softwareDownload struct {
	entityID        uint16
	device          omci.DeviceIdent
	expectedSize    uint32
	received        uint32
	windowSize      uint16
	expectedSection uint8
	sink            software.Download
	crc             uint32
	md5             hash.Hash
}

type SoftwareStatus struct {
	Phase     string `json:"phase"`
	ImageID   uint16 `json:"image_id"`
	Bytes     uint32 `json:"bytes"`
	ImageSize uint32 `json:"image_size"`
	ImageHash string `json:"image_hash,omitempty"`
}

func (e *Engine) SoftwareStatus() SoftwareStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.softwareState
}

// RefreshSoftwareImages imports persistent boot-slot state before OMCC
// traffic starts. It does not change MIB data sync because these are
// autonomous ONU attributes.
func (e *Engine) RefreshSoftwareImages() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.software == nil {
		return nil
	}
	images, err := e.software.Images()
	if err != nil {
		return err
	}
	if len(images) != 2 {
		return fmt.Errorf("software backend returned %d images, want 2", len(images))
	}
	seen := make(map[uint16]struct{}, len(images))
	active, committed := 0, 0
	for _, image := range images {
		if image.EntityID > 1 || len(image.Version) > 14 || len(image.ProductCode) > 25 {
			return fmt.Errorf("software backend returned invalid image metadata for %#x", image.EntityID)
		}
		if _, duplicate := seen[image.EntityID]; duplicate {
			return fmt.Errorf("software backend returned duplicate image %#x", image.EntityID)
		}
		seen[image.EntityID] = struct{}{}
		if image.Active {
			active++
		}
		if image.Committed {
			committed++
		}
		if (image.Active || image.Committed) && !image.Valid {
			return fmt.Errorf("software backend returned invalid selected image %#x", image.EntityID)
		}
	}
	if active != 1 || committed != 1 {
		return fmt.Errorf("software backend returned inconsistent image flags")
	}
	for _, image := range images {
		attributes := me.AttributeValueMap{
			me.SoftwareImage_Version:     fixedOctets(image.Version, 14),
			me.SoftwareImage_IsCommitted: boolOctet(image.Committed),
			me.SoftwareImage_IsActive:    boolOctet(image.Active),
			me.SoftwareImage_IsValid:     boolOctet(image.Valid),
			me.SoftwareImage_ProductCode: fixedOctets(image.ProductCode, 25),
			me.SoftwareImage_ImageHash:   append([]byte(nil), image.ImageHash[:]...),
		}
		if _, err := e.mib.UpdateAutonomous(softwareKey(image.EntityID), attributes); err != nil {
			return fmt.Errorf("import software image %#x: %w", image.EntityID, err)
		}
	}
	return nil
}

func (e *Engine) startSoftwareDownload(request *omci.StartSoftwareDownloadRequest,
	device omci.DeviceIdent) me.Results {
	if e.software == nil {
		return me.NotSupported
	}
	if e.download != nil {
		return me.DeviceBusy
	}
	if request.EntityClass != me.SoftwareImageClassID || request.EntityInstance > 1 ||
		request.ImageSize == 0 || request.NumberOfCircuitPacks != 1 ||
		len(request.CircuitPacks) != 1 || request.CircuitPacks[0] != request.EntityInstance {
		return me.ParameterError
	}
	image, result := e.softwareImage(request.EntityInstance)
	if result != me.Success {
		return result
	}
	if image.active {
		return me.ParameterError
	}
	sink, err := e.software.Start(request.EntityInstance, request.ImageSize)
	if err != nil {
		return me.ProcessingError
	}
	if _, err := e.mib.UpdateAutonomous(softwareKey(request.EntityInstance), me.AttributeValueMap{
		me.SoftwareImage_IsValid: uint8(0),
	}); err != nil {
		_ = sink.Abort()
		return me.ProcessingError
	}
	e.download = &softwareDownload{
		entityID: request.EntityInstance, device: device,
		expectedSize: request.ImageSize, windowSize: uint16(request.WindowSize) + 1,
		sink: sink, crc: checksum.CRC32AInitial, md5: md5.New(),
	}
	e.softwareState = SoftwareStatus{
		Phase: "downloading", ImageID: request.EntityInstance, ImageSize: request.ImageSize,
	}
	return me.Success
}

func (e *Engine) downloadSoftwareSection(request *omci.DownloadSectionRequest,
	device omci.DeviceIdent) me.Results {
	download := e.download
	if download == nil {
		return me.ProcessingError
	}
	if request.EntityClass != me.SoftwareImageClassID ||
		request.EntityInstance != download.entityID || device != download.device ||
		request.SectionNumber != download.expectedSection || len(request.SectionData) == 0 ||
		download.received >= download.expectedSize {
		e.failSoftwareDownload()
		return me.ParameterError
	}
	remaining := int(download.expectedSize - download.received)
	data := request.SectionData
	if len(data) > remaining {
		data = data[:remaining]
	}
	written, err := download.sink.Write(data)
	if err != nil || written != len(data) {
		e.failSoftwareDownload()
		return me.ProcessingError
	}
	download.crc = checksum.UpdateCRC32A(download.crc, data)
	_, _ = download.md5.Write(data)
	download.received += uint32(len(data))
	next := uint16(download.expectedSection) + 1
	if next >= download.windowSize {
		download.expectedSection = 0
	} else {
		download.expectedSection = uint8(next)
	}
	e.softwareState.Bytes = download.received
	return me.Success
}

func (e *Engine) endSoftwareDownload(request *omci.EndSoftwareDownloadRequest,
	device omci.DeviceIdent) me.Results {
	download := e.download
	if download == nil {
		return me.ProcessingError
	}
	validRequest := request.EntityClass == me.SoftwareImageClassID &&
		request.EntityInstance == download.entityID && device == download.device &&
		request.ImageSize == download.expectedSize && request.ImageSize == download.received &&
		request.NumberOfInstances == 1 && len(request.ImageInstances) == 1 &&
		request.ImageInstances[0] == download.entityID
	if !validRequest || request.CRC32 != checksum.SumCRC32A(download.crc) {
		e.failSoftwareDownload()
		return me.ProcessingError
	}
	metadata, err := download.sink.Finish()
	if err != nil || len(metadata.Version) > 14 || len(metadata.ProductCode) > 25 {
		e.download = nil
		e.softwareState.Phase = "failed"
		return me.ProcessingError
	}
	if strings.TrimSpace(metadata.ProductCode) == "" {
		metadata.ProductCode = "XG2010G"
	}
	hashValue := download.md5.Sum(nil)
	_, err = e.mib.UpdateAutonomous(softwareKey(download.entityID), me.AttributeValueMap{
		me.SoftwareImage_Version:     fixedOctets(metadata.Version, 14),
		me.SoftwareImage_IsValid:     uint8(1),
		me.SoftwareImage_ProductCode: fixedOctets(metadata.ProductCode, 25),
		me.SoftwareImage_ImageHash:   append([]byte(nil), hashValue...),
	})
	e.download = nil
	if err != nil {
		e.softwareState.Phase = "failed"
		return me.ProcessingError
	}
	e.softwareState.Phase = "staged"
	e.softwareState.Bytes = request.ImageSize
	e.softwareState.ImageHash = hex.EncodeToString(hashValue)
	return me.Success
}

func (e *Engine) activateSoftware(request *omci.ActivateSoftwareRequest) me.Results {
	if e.software == nil {
		return me.NotSupported
	}
	if e.download != nil {
		return me.DeviceBusy
	}
	if request.EntityClass != me.SoftwareImageClassID || request.EntityInstance > 1 {
		return me.ParameterError
	}
	image, result := e.softwareImage(request.EntityInstance)
	if result != me.Success {
		return result
	}
	if !image.valid {
		return me.ParameterError
	}
	if image.active {
		return me.Success
	}
	if err := e.software.Activate(request.EntityInstance, request.ActivateFlags); err != nil {
		return me.ProcessingError
	}
	if err := e.setExclusiveSoftwareFlag(request.EntityInstance, me.SoftwareImage_IsActive); err != nil {
		return me.ProcessingError
	}
	e.softwareState.Phase = "activating"
	e.softwareState.ImageID = request.EntityInstance
	return me.Success
}

func (e *Engine) commitSoftware(request *omci.CommitSoftwareRequest) me.Results {
	if e.software == nil {
		return me.NotSupported
	}
	if e.download != nil {
		return me.DeviceBusy
	}
	if request.EntityClass != me.SoftwareImageClassID || request.EntityInstance > 1 {
		return me.ParameterError
	}
	image, result := e.softwareImage(request.EntityInstance)
	if result != me.Success {
		return result
	}
	if !image.valid || !image.active {
		return me.ParameterError
	}
	if image.committed {
		return me.Success
	}
	if err := e.software.Commit(request.EntityInstance); err != nil {
		return me.ProcessingError
	}
	if err := e.setExclusiveSoftwareFlag(request.EntityInstance, me.SoftwareImage_IsCommitted); err != nil {
		return me.ProcessingError
	}
	e.softwareState.Phase = "committed"
	e.softwareState.ImageID = request.EntityInstance
	return me.Success
}

func (e *Engine) failSoftwareDownload() {
	if e.download != nil {
		_ = e.download.sink.Abort()
	}
	e.download = nil
	e.softwareState.Phase = "failed"
}

type imageFlags struct {
	active    bool
	committed bool
	valid     bool
}

func (e *Engine) softwareImage(entityID uint16) (imageFlags, me.Results) {
	instance, err := e.mib.Get(softwareKey(entityID), 0x7000)
	result, _, _ := operationResult(err)
	if result != me.Success {
		return imageFlags{}, result
	}
	active, activeOK := instance.Attributes[me.SoftwareImage_IsActive].(uint8)
	committed, committedOK := instance.Attributes[me.SoftwareImage_IsCommitted].(uint8)
	valid, validOK := instance.Attributes[me.SoftwareImage_IsValid].(uint8)
	if !activeOK || !committedOK || !validOK {
		return imageFlags{}, me.ProcessingError
	}
	return imageFlags{active: active == 1, committed: committed == 1, valid: valid == 1}, me.Success
}

func (e *Engine) setExclusiveSoftwareFlag(selected uint16, attribute string) error {
	for entityID := uint16(0); entityID <= 1; entityID++ {
		value := uint8(0)
		if entityID == selected {
			value = 1
		}
		if _, err := e.mib.UpdateAutonomous(softwareKey(entityID), me.AttributeValueMap{
			attribute: value,
		}); err != nil {
			return err
		}
	}
	return nil
}

func softwareResults(instances []uint16, result me.Results) []omci.DownloadResults {
	results := make([]omci.DownloadResults, len(instances))
	for index, entityID := range instances {
		results[index] = omci.DownloadResults{ManagedEntityID: entityID, Result: result}
	}
	return results
}

func softwareKey(entityID uint16) mib.Key {
	return mib.Key{ClassID: me.SoftwareImageClassID, EntityID: entityID}
}

func boolOctet(value bool) uint8 {
	if value {
		return 1
	}
	return 0
}

func fixedOctets(value string, size int) []byte {
	result := make([]byte, size)
	copy(result, strings.TrimSpace(value))
	return result
}
