// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"bytes"
	"crypto/md5"
	"testing"

	omci "github.com/opencord/omci-lib-go/v2"
	me "github.com/opencord/omci-lib-go/v2/generated"
	"github.com/xg2010g/airoha-omci/internal/checksum"
	"github.com/xg2010g/airoha-omci/internal/mib"
	"github.com/xg2010g/airoha-omci/internal/model"
	"github.com/xg2010g/airoha-omci/internal/software"
)

type recordingSoftwareController struct {
	images        []software.Image
	download      *recordingDownload
	startedID     uint16
	startedSize   uint32
	activatedID   uint16
	activateFlags uint8
	committedID   uint16
}

func (c *recordingSoftwareController) Images() ([]software.Image, error) {
	return append([]software.Image(nil), c.images...), nil
}

func (c *recordingSoftwareController) Start(entityID uint16, imageSize uint32) (software.Download, error) {
	c.startedID = entityID
	c.startedSize = imageSize
	c.download = &recordingDownload{}
	return c.download, nil
}

func (c *recordingSoftwareController) Activate(entityID uint16, flags uint8) error {
	c.activatedID = entityID
	c.activateFlags = flags
	return nil
}

func (c *recordingSoftwareController) Commit(entityID uint16) error {
	c.committedID = entityID
	return nil
}

type recordingDownload struct {
	bytes.Buffer
	finished bool
	aborted  bool
}

func (d *recordingDownload) Finish() (software.Metadata, error) {
	d.finished = true
	return software.Metadata{Version: "2026.08", ProductCode: "XG2010G"}, nil
}

func (d *recordingDownload) Abort() error {
	d.aborted = true
	return nil
}

func TestSoftwareDownloadLifecycleAndNoResponseReplay(t *testing.T) {
	protocol, store, controller := newSoftwareTestEngine(t)
	image := make([]byte, 35)
	for index := range image {
		image[index] = byte(index + 1)
	}

	start := encodeRequest(t, 20, omci.StartSoftwareDownloadRequestType, &omci.StartSoftwareDownloadRequest{
		MeBasePacket: omci.MeBasePacket{EntityClass: me.SoftwareImageClassID, EntityInstance: 1},
		WindowSize:   1, ImageSize: uint32(len(image)), NumberOfCircuitPacks: 1,
		CircuitPacks: []uint16{1},
	})
	encoded, err := protocol.Handle(start)
	if err != nil {
		t.Fatalf("Handle(StartSoftwareDownload) error = %v", err)
	}
	startResponse := decodeResponse(t, encoded).Layer(omci.LayerTypeStartSoftwareDownloadResponse).(*omci.StartSoftwareDownloadResponse)
	if startResponse.Result != me.Success || controller.startedID != 1 || controller.startedSize != uint32(len(image)) {
		t.Fatalf("start response/controller = %#v %#v", startResponse, controller)
	}

	firstSection := encodeRequest(t, 21, omci.DownloadSectionRequestType, &omci.DownloadSectionRequest{
		MeBasePacket:  omci.MeBasePacket{EntityClass: me.SoftwareImageClassID, EntityInstance: 1},
		SectionNumber: 0, SectionData: image[:omci.MaxDownloadSectionLength],
	})
	if encoded, err = protocol.Handle(firstSection); err != nil || encoded != nil {
		t.Fatalf("Handle(no-response section) = %x, %v; want nil, nil", encoded, err)
	}
	if encoded, err = protocol.Handle(firstSection); err != nil || encoded != nil {
		t.Fatalf("Handle(duplicate no-response section) = %x, %v; want nil, nil", encoded, err)
	}
	if got := controller.download.Len(); got != omci.MaxDownloadSectionLength {
		t.Fatalf("duplicate section wrote %d bytes, want %d", got, omci.MaxDownloadSectionLength)
	}

	lastSection := encodeRequest(t, 22, omci.DownloadSectionRequestWithResponseType, &omci.DownloadSectionRequest{
		MeBasePacket:  omci.MeBasePacket{EntityClass: me.SoftwareImageClassID, EntityInstance: 1},
		SectionNumber: 1, SectionData: image[omci.MaxDownloadSectionLength:],
	})
	encoded, err = protocol.Handle(lastSection)
	if err != nil {
		t.Fatalf("Handle(last section) error = %v", err)
	}
	sectionResponse := decodeResponse(t, encoded).Layer(omci.LayerTypeDownloadSectionResponse).(*omci.DownloadSectionResponse)
	if sectionResponse.Result != me.Success || !bytes.Equal(controller.download.Bytes(), image) {
		t.Fatalf("last section result/data = %v/%x", sectionResponse.Result, controller.download.Bytes())
	}

	end := encodeRequest(t, 23, omci.EndSoftwareDownloadRequestType, &omci.EndSoftwareDownloadRequest{
		MeBasePacket: omci.MeBasePacket{EntityClass: me.SoftwareImageClassID, EntityInstance: 1},
		CRC32:        checksum.CRC32A(image), ImageSize: uint32(len(image)),
		NumberOfInstances: 1, ImageInstances: []uint16{1},
	})
	encoded, err = protocol.Handle(end)
	if err != nil {
		t.Fatalf("Handle(EndSoftwareDownload) error = %v", err)
	}
	endResponse := decodeResponse(t, encoded).Layer(omci.LayerTypeEndSoftwareDownloadResponse).(*omci.EndSoftwareDownloadResponse)
	if endResponse.Result != me.Success || !controller.download.finished {
		t.Fatalf("end response/finished = %#v/%v", endResponse, controller.download.finished)
	}
	instance, err := store.Get(softwareKey(1), 0xfc00)
	if err != nil {
		t.Fatalf("Get(software image) error = %v", err)
	}
	wantHash := md5.Sum(image)
	if instance.Attributes[me.SoftwareImage_IsValid] != uint8(1) ||
		!bytes.Equal(instance.Attributes[me.SoftwareImage_ImageHash].([]byte), wantHash[:]) {
		t.Fatalf("software image attributes = %#v", instance.Attributes)
	}

	activate := encodeRequest(t, 24, omci.ActivateSoftwareRequestType, &omci.ActivateSoftwareRequest{
		MeBasePacket:  omci.MeBasePacket{EntityClass: me.SoftwareImageClassID, EntityInstance: 1},
		ActivateFlags: 2,
	})
	encoded, err = protocol.Handle(activate)
	if err != nil {
		t.Fatalf("Handle(ActivateSoftware) error = %v", err)
	}
	if response := decodeResponse(t, encoded).Layer(omci.LayerTypeActivateSoftwareResponse).(*omci.ActivateSoftwareResponse); response.Result != me.Success || controller.activatedID != 1 || controller.activateFlags != 2 {
		t.Fatalf("activate response/controller = %#v/%#v", response, controller)
	}

	commit := encodeRequest(t, 25, omci.CommitSoftwareRequestType, &omci.CommitSoftwareRequest{
		MeBasePacket: omci.MeBasePacket{EntityClass: me.SoftwareImageClassID, EntityInstance: 1},
	})
	encoded, err = protocol.Handle(commit)
	if err != nil {
		t.Fatalf("Handle(CommitSoftware) error = %v", err)
	}
	if response := decodeResponse(t, encoded).Layer(omci.LayerTypeCommitSoftwareResponse).(*omci.CommitSoftwareResponse); response.Result != me.Success || controller.committedID != 1 {
		t.Fatalf("commit response/controller = %#v/%#v", response, controller)
	}
	assertSoftwareFlag(t, store, 0, me.SoftwareImage_IsActive, 0)
	assertSoftwareFlag(t, store, 1, me.SoftwareImage_IsActive, 1)
	assertSoftwareFlag(t, store, 0, me.SoftwareImage_IsCommitted, 0)
	assertSoftwareFlag(t, store, 1, me.SoftwareImage_IsCommitted, 1)
}

func TestSoftwareDownloadRejectsOutOfOrderSectionAndAborts(t *testing.T) {
	protocol, _, controller := newSoftwareTestEngine(t)
	start := encodeRequest(t, 30, omci.StartSoftwareDownloadRequestType, &omci.StartSoftwareDownloadRequest{
		MeBasePacket: omci.MeBasePacket{EntityClass: me.SoftwareImageClassID, EntityInstance: 1},
		WindowSize:   3, ImageSize: 31, NumberOfCircuitPacks: 1, CircuitPacks: []uint16{1},
	})
	if _, err := protocol.Handle(start); err != nil {
		t.Fatalf("Handle(StartSoftwareDownload) error = %v", err)
	}
	section := encodeRequest(t, 31, omci.DownloadSectionRequestWithResponseType, &omci.DownloadSectionRequest{
		MeBasePacket:  omci.MeBasePacket{EntityClass: me.SoftwareImageClassID, EntityInstance: 1},
		SectionNumber: 1, SectionData: make([]byte, 31),
	})
	encoded, err := protocol.Handle(section)
	if err != nil {
		t.Fatalf("Handle(out-of-order section) error = %v", err)
	}
	response := decodeResponse(t, encoded).Layer(omci.LayerTypeDownloadSectionResponse).(*omci.DownloadSectionResponse)
	if response.Result != me.ParameterError || !controller.download.aborted {
		t.Fatalf("section result/aborted = %v/%v", response.Result, controller.download.aborted)
	}
	if status := protocol.SoftwareStatus(); status.Phase != "failed" {
		t.Fatalf("SoftwareStatus() = %#v, want failed", status)
	}
}

func TestRefreshSoftwareImagesImportsPersistentFlags(t *testing.T) {
	protocol, store, controller := newSoftwareTestEngine(t)
	controller.images = []software.Image{
		{EntityID: 0, Version: "old", ProductCode: "XG2010G", Valid: true},
		{EntityID: 1, Version: "new", ProductCode: "XG2010G", Active: true, Committed: true, Valid: true},
	}
	if err := protocol.RefreshSoftwareImages(); err != nil {
		t.Fatalf("RefreshSoftwareImages() error = %v", err)
	}
	assertSoftwareFlag(t, store, 0, me.SoftwareImage_IsActive, 0)
	assertSoftwareFlag(t, store, 1, me.SoftwareImage_IsActive, 1)
	assertSoftwareFlag(t, store, 1, me.SoftwareImage_IsCommitted, 1)
	if store.DataSync() != 0 {
		t.Fatalf("RefreshSoftwareImages changed MIB data sync to %d", store.DataSync())
	}
}

func TestRefreshSoftwareImagesRejectsInvalidSelectedImage(t *testing.T) {
	protocol, _, controller := newSoftwareTestEngine(t)
	controller.images = []software.Image{
		{EntityID: 0, Version: "bad", ProductCode: "XG2010G", Active: true, Committed: true},
		{EntityID: 1, Version: "new", ProductCode: "XG2010G", Valid: true},
	}
	if err := protocol.RefreshSoftwareImages(); err == nil {
		t.Fatal("RefreshSoftwareImages() accepted invalid active/committed image")
	}
}

func newSoftwareTestEngine(t *testing.T) (*Engine, *mib.Store, *recordingSoftwareController) {
	t.Helper()
	factory, err := model.XG2010G(model.Identity{SerialNumber: "TEST01020304", Version: "old"})
	if err != nil {
		t.Fatalf("model.XG2010G() error = %v", err)
	}
	store, err := mib.New(factory)
	if err != nil {
		t.Fatalf("mib.New() error = %v", err)
	}
	controller := &recordingSoftwareController{}
	return NewWithControllers(store, nil, controller), store, controller
}

func assertSoftwareFlag(t *testing.T, store *mib.Store, entityID uint16, attribute string, want uint8) {
	t.Helper()
	instance, err := store.Get(softwareKey(entityID), 0x7000)
	if err != nil {
		t.Fatalf("Get(software image %#x) error = %v", entityID, err)
	}
	if got := instance.Attributes[attribute]; got != want {
		t.Fatalf("software image %#x %s = %#v, want %d", entityID, attribute, got, want)
	}
}
