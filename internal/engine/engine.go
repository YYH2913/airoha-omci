// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/gopacket"
	omci "github.com/opencord/omci-lib-go/v2"
	me "github.com/opencord/omci-lib-go/v2/generated"
	"github.com/xg2010g/airoha-omci/internal/mib"
	"github.com/xg2010g/airoha-omci/internal/optical"
	"github.com/xg2010g/airoha-omci/internal/software"
)

const (
	baselineLowPriority transactionChannel = iota
	baselineHighPriority
	extendedPriority
	transactionChannelCount

	mibUploadTimeout = time.Minute
)

type transactionChannel uint8

type transactionReplay struct {
	valid         bool
	transactionID uint16
	response      []byte
	refreshUpload bool
}

type uploadCommand []me.ManagedEntity

type uploadSession struct {
	commands []uploadCommand
	expires  time.Time
}

type tableKey struct {
	device   omci.DeviceIdent
	classID  me.ClassID
	entityID uint16
	mask     uint16
}

type Alarm struct {
	Key    mib.Key
	Bitmap [28]byte
}

type Controller interface {
	SynchronizeTime(time.Time) error
	Reboot(uint8) error
	OpticalLineSupervision() (optical.Diagnostics, error)
}

type Engine struct {
	mu sync.Mutex

	mib *mib.Store

	transactions  [transactionChannelCount]transactionReplay
	oneWayDigest  [sha256.Size]byte
	oneWayValid   bool
	upload        map[omci.DeviceIdent]uploadSession
	tables        map[tableKey][]byte
	alarms        map[mib.Key][28]byte
	alarmUpload   map[omci.DeviceIdent][]Alarm
	alarmSequence uint8
	arcFreeSince  map[mib.Key]time.Time
	opticalSample map[mib.Key]optical.Sample
	now           func() time.Time
	controller    Controller
	software      software.Controller
	download      *softwareDownload
	softwareState SoftwareStatus
	pending       [][]byte
	pendingError  error
}

func New(store *mib.Store) *Engine {
	return NewWithController(store, nil)
}

func NewWithController(store *mib.Store, controller Controller) *Engine {
	return NewWithControllers(store, controller, nil)
}

func NewWithControllers(store *mib.Store, controller Controller, softwareController software.Controller) *Engine {
	return &Engine{
		mib:           store,
		upload:        make(map[omci.DeviceIdent]uploadSession),
		tables:        make(map[tableKey][]byte),
		alarms:        make(map[mib.Key][28]byte),
		alarmUpload:   make(map[omci.DeviceIdent][]Alarm),
		arcFreeSince:  make(map[mib.Key]time.Time),
		opticalSample: make(map[mib.Key]optical.Sample),
		now:           time.Now,
		controller:    controller,
		software:      softwareController,
		softwareState: SoftwareStatus{Phase: "idle"},
	}
}

// SetAlarm updates the alarm table used by Get All Alarms. A zero bitmap
// clears the entry. Autonomous notification transport is handled separately.
func (e *Engine) SetAlarm(key mib.Key, bitmap [28]byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if bitmap == ([28]byte{}) {
		delete(e.alarms, key)
		return
	}
	e.alarms[key] = bitmap
}

// DrainNotifications returns notifications produced while handling the last
// request. The caller sends them only after the solicited response.
func (e *Engine) DrainNotifications() [][]byte {
	e.mu.Lock()
	defer e.mu.Unlock()

	frames := e.pending
	e.pending = nil
	return frames
}

// DrainNotificationError reports a failure that occurred while deriving an
// autonomous notification from an already committed OLT command. The command
// response remains successful so a retransmission cannot apply it twice.
func (e *Engine) DrainNotificationError() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	err := e.pendingError
	e.pendingError = nil
	return err
}

// Handle processes one complete downstream OMCI frame and returns the upstream
// response. G.988 stop-and-wait replay is tracked independently for baseline
// low/high priority and for the single extended-message priority class.
func (e *Engine) Handle(frame []byte) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := validateFrame(frame); err != nil {
		return nil, err
	}
	packet := gopacket.NewPacket(frame, omci.LayerTypeOMCI, gopacket.Default)
	headerLayer := packet.Layer(omci.LayerTypeOMCI)
	if headerLayer == nil {
		return nil, errors.New("OMCI header is missing")
	}
	header, ok := headerLayer.(*omci.OMCI)
	if !ok {
		return nil, errors.New("invalid OMCI header layer")
	}
	noResponseDownload := header.MessageType == omci.DownloadSectionRequestType
	if byte(header.MessageType)&me.AK != 0 ||
		(byte(header.MessageType)&me.AR == 0 && !noResponseDownload) {
		return nil, fmt.Errorf("unexpected downstream message type %#x", byte(header.MessageType))
	}
	if response, found := e.replayTransaction(header); found {
		return response, nil
	}
	digest := sha256.Sum256(frame)
	if noResponseDownload && e.oneWayValid && digest == e.oneWayDigest {
		return nil, nil
	}
	if response, handled, err := e.handleRawSpecial(header, frame); handled {
		if err != nil {
			return nil, err
		}
		e.rememberTransaction(header, response)
		return append([]byte(nil), response...), nil
	}
	if decodeError := packet.ErrorLayer(); decodeError != nil {
		response, handled, err := e.decodeFailureResponse(header, frame, decodeError.Error())
		if err != nil {
			return nil, err
		}
		if !handled {
			return nil, fmt.Errorf("decode OMCI request: %w", decodeError.Error())
		}
		e.rememberTransaction(header, response)
		return append([]byte(nil), response...), nil
	}

	response, err := e.dispatch(packet, header)
	if err != nil {
		return nil, err
	}
	if noResponseDownload {
		e.oneWayDigest = digest
		e.oneWayValid = true
	} else {
		e.rememberTransaction(header, response)
	}
	return append([]byte(nil), response...), nil
}

func (e *Engine) dispatch(packet gopacket.Packet, header *omci.OMCI) ([]byte, error) {
	extended := header.DeviceIdentifier == omci.ExtendedIdent

	switch header.MessageType {
	case omci.CreateRequestType:
		request, err := layerAs[*omci.CreateRequest](packet, omci.LayerTypeCreateRequest)
		if err != nil {
			return nil, err
		}
		operationError := e.mib.Create(request.EntityClass, request.EntityInstance, request.Attributes)
		result, failed, _ := operationResult(operationError)
		if result == me.Success {
			e.tables = make(map[tableKey][]byte)
		}
		return serialize(header, omci.CreateResponseType, &omci.CreateResponse{
			MeBasePacket: omci.MeBasePacket{
				EntityClass:    request.EntityClass,
				EntityInstance: request.EntityInstance,
				Extended:       extended,
			},
			Result:                 result,
			AttributeExecutionMask: failed,
		})

	case omci.DeleteRequestType:
		request, err := layerAs[*omci.DeleteRequest](packet, omci.LayerTypeDeleteRequest)
		if err != nil {
			return nil, err
		}
		result, _, _ := operationResult(e.mib.Delete(mib.Key{
			ClassID:  request.EntityClass,
			EntityID: request.EntityInstance,
		}))
		if result == me.Success {
			e.tables = make(map[tableKey][]byte)
		}
		return serialize(header, omci.DeleteResponseType, &omci.DeleteResponse{
			MeBasePacket: omci.MeBasePacket{
				EntityClass:    request.EntityClass,
				EntityInstance: request.EntityInstance,
				Extended:       extended,
			},
			Result: result,
		})

	case omci.SetRequestType:
		request, err := layerAs[*omci.SetRequest](packet, omci.LayerTypeSetRequest)
		if err != nil {
			return nil, err
		}
		key := mib.Key{
			ClassID:  request.EntityClass,
			EntityID: request.EntityInstance,
		}
		result, failed, unsupported := operationResult(e.mib.Set(key, request.Attributes))
		if result == me.Success {
			e.tables = make(map[tableKey][]byte)
			notifications, notifyErr := e.afterSetLocked(key, request.Attributes, header.DeviceIdentifier)
			if notifyErr != nil {
				e.pendingError = fmt.Errorf("derive notifications after Set %v/%#x: %w",
					key.ClassID, key.EntityID, notifyErr)
			} else {
				e.pending = append(e.pending, notifications...)
			}
		}
		return serialize(header, omci.SetResponseType, &omci.SetResponse{
			MeBasePacket: omci.MeBasePacket{
				EntityClass:    request.EntityClass,
				EntityInstance: request.EntityInstance,
				Extended:       extended,
			},
			Result:                   result,
			FailedAttributeMask:      failed,
			UnsupportedAttributeMask: unsupported,
		})

	case omci.GetRequestType:
		request, err := layerAs[*omci.GetRequest](packet, omci.LayerTypeGetRequest)
		if err != nil {
			return nil, err
		}
		instance, operationError := e.mib.Get(mib.Key{
			ClassID:  request.EntityClass,
			EntityID: request.EntityInstance,
		}, request.AttributeMask)
		result, failed, unsupported := operationResult(operationError)
		if instance.Attributes != nil {
			if err := e.prepareTableGet(&instance, request.AttributeMask, header.DeviceIdentifier); err != nil {
				return nil, err
			}
		}
		return serialize(header, omci.GetResponseType, &omci.GetResponse{
			MeBasePacket: omci.MeBasePacket{
				EntityClass:    request.EntityClass,
				EntityInstance: request.EntityInstance,
				Extended:       extended,
			},
			Result:                   result,
			AttributeMask:            request.AttributeMask &^ unsupported &^ failed,
			Attributes:               instance.Attributes,
			FailedAttributeMask:      failed,
			UnsupportedAttributeMask: unsupported,
		})

	case omci.MibResetRequestType:
		request, err := layerAs[*omci.MibResetRequest](packet, omci.LayerTypeMibResetRequest)
		if err != nil {
			return nil, err
		}
		result, _, _ := operationResult(e.mib.Reset())
		if result == me.Success {
			e.upload = make(map[omci.DeviceIdent]uploadSession)
			e.tables = make(map[tableKey][]byte)
			e.arcFreeSince = make(map[mib.Key]time.Time)
		}
		return serialize(header, omci.MibResetResponseType, &omci.MibResetResponse{
			MeBasePacket: omci.MeBasePacket{
				EntityClass:    request.EntityClass,
				EntityInstance: request.EntityInstance,
				Extended:       extended,
			},
			Result: result,
		})

	case omci.MibUploadRequestType:
		request, err := layerAs[*omci.MibUploadRequest](packet, omci.LayerTypeMibUploadRequest)
		if err != nil {
			return nil, err
		}
		commands, err := buildUpload(e.mib.Snapshot(), header.DeviceIdentifier)
		if err != nil {
			return nil, err
		}
		e.upload[header.DeviceIdentifier] = uploadSession{
			commands: commands,
			expires:  e.now().Add(mibUploadTimeout),
		}
		return serialize(header, omci.MibUploadResponseType, &omci.MibUploadResponse{
			MeBasePacket: omci.MeBasePacket{
				EntityClass:    request.EntityClass,
				EntityInstance: request.EntityInstance,
				Extended:       extended,
			},
			NumberOfCommands: uint16(len(commands)),
		})

	case omci.MibUploadNextRequestType:
		request, err := layerAs[*omci.MibUploadNextRequest](packet, omci.LayerTypeMibUploadNextRequest)
		if err != nil {
			return nil, err
		}
		now := e.now()
		session, present := e.upload[header.DeviceIdentifier]
		if !present || !now.Before(session.expires) ||
			int(request.CommandSequenceNumber) >= len(session.commands) {
			if present && !now.Before(session.expires) {
				delete(e.upload, header.DeviceIdentifier)
			}
			return serializeEmptyMibUploadNext(header, request.EntityClass, request.EntityInstance)
		}
		session.expires = now.Add(mibUploadTimeout)
		e.upload[header.DeviceIdentifier] = session
		command := session.commands[request.CommandSequenceNumber]
		return serialize(header, omci.MibUploadNextResponseType, &omci.MibUploadNextResponse{
			MeBasePacket: omci.MeBasePacket{
				EntityClass:    request.EntityClass,
				EntityInstance: request.EntityInstance,
				Extended:       extended,
			},
			ReportedME:    command[0],
			AdditionalMEs: append([]me.ManagedEntity(nil), command[1:]...),
		})

	case omci.GetAllAlarmsRequestType:
		request, err := layerAs[*omci.GetAllAlarmsRequest](packet, omci.LayerTypeGetAllAlarmsRequest)
		if err != nil {
			return nil, err
		}
		alarms := make([]Alarm, 0, len(e.alarms))
		for key, bitmap := range e.alarms {
			if request.AlarmRetrievalMode == 1 && e.arcEnabledLocked(key) {
				continue
			}
			alarms = append(alarms, Alarm{Key: key, Bitmap: bitmap})
		}
		sort.Slice(alarms, func(i, j int) bool {
			if alarms[i].Key.ClassID == alarms[j].Key.ClassID {
				return alarms[i].Key.EntityID < alarms[j].Key.EntityID
			}
			return alarms[i].Key.ClassID < alarms[j].Key.ClassID
		})
		e.alarmSequence = 0
		e.alarmUpload[header.DeviceIdentifier] = alarms
		return serialize(header, omci.GetAllAlarmsResponseType, &omci.GetAllAlarmsResponse{
			MeBasePacket: omci.MeBasePacket{
				EntityClass:    request.EntityClass,
				EntityInstance: request.EntityInstance,
				Extended:       extended,
			},
			NumberOfCommands: uint16(len(alarms)),
		})

	case omci.GetAllAlarmsNextRequestType:
		request, err := layerAs[*omci.GetAllAlarmsNextRequest](packet, omci.LayerTypeGetAllAlarmsNextRequest)
		if err != nil {
			return nil, err
		}
		response := &omci.GetAllAlarmsNextResponse{
			MeBasePacket: omci.MeBasePacket{
				EntityClass:    request.EntityClass,
				EntityInstance: request.EntityInstance,
				Extended:       extended,
			},
		}
		alarms := e.alarmUpload[header.DeviceIdentifier]
		if int(request.CommandSequenceNumber) < len(alarms) {
			alarm := alarms[request.CommandSequenceNumber]
			response.AlarmEntityClass = alarm.Key.ClassID
			response.AlarmEntityInstance = alarm.Key.EntityID
			response.AlarmBitMap = alarm.Bitmap
		}
		return serialize(header, omci.GetAllAlarmsNextResponseType, response)

	case omci.GetNextRequestType:
		request, err := layerAs[*omci.GetNextRequest](packet, omci.LayerTypeGetNextRequest)
		if err != nil {
			return nil, err
		}
		key := tableKey{
			device:   header.DeviceIdentifier,
			classID:  request.EntityClass,
			entityID: request.EntityInstance,
			mask:     request.AttributeMask,
		}
		data, found := e.tables[key]
		result := me.Success
		attributes := make(me.AttributeValueMap)
		limit := omci.MaxAttributeGetNextBaselineLength
		if extended {
			limit = omci.MaxAttributeGetNextExtendedLength
		}
		offset := int(request.SequenceNumber) * limit
		if !found || offset >= len(data) {
			result = me.ProcessingError
		} else {
			end := offset + limit
			if end > len(data) {
				end = len(data)
			}
			definition, omciErr := me.LoadManagedEntityDefinition(request.EntityClass, me.ParamData{EntityID: request.EntityInstance})
			if omciErr.StatusCode() != me.Success {
				result = omciErr.StatusCode()
			} else {
				for index, attribute := range definition.GetAttributeDefinitions() {
					if index != 0 && attribute.Mask == request.AttributeMask {
						attributes[attribute.GetName()] = append([]byte(nil), data[offset:end]...)
						break
					}
				}
				if len(attributes) == 0 {
					result = me.AttributeFailure
				}
			}
		}
		return serialize(header, omci.GetNextResponseType, &omci.GetNextResponse{
			MeBasePacket: omci.MeBasePacket{
				EntityClass:    request.EntityClass,
				EntityInstance: request.EntityInstance,
				Extended:       extended,
			},
			Result:        result,
			AttributeMask: request.AttributeMask,
			Attributes:    attributes,
		})

	case omci.SetTableRequestType:
		request, err := layerAs[*omci.SetTableRequest](packet, omci.LayerTypeSetTableRequest)
		if err != nil {
			return nil, err
		}
		result, _, _ := operationResult(e.mib.SetTable(mib.Key{
			ClassID:  request.EntityClass,
			EntityID: request.EntityInstance,
		}, request.AttributeMask, request.Attributes))
		if result == me.Success {
			e.tables = make(map[tableKey][]byte)
		}
		return serialize(header, omci.SetTableResponseType, &omci.SetTableResponse{
			MeBasePacket: omci.MeBasePacket{
				EntityClass:    request.EntityClass,
				EntityInstance: request.EntityInstance,
				Extended:       extended,
			},
			Result: result,
		})

	case omci.GetCurrentDataRequestType:
		request, err := layerAs[*omci.GetCurrentDataRequest](packet, omci.LayerTypeGetCurrentDataRequest)
		if err != nil {
			return nil, err
		}
		instance, operationError := e.mib.Get(mib.Key{
			ClassID:  request.EntityClass,
			EntityID: request.EntityInstance,
		}, request.AttributeMask)
		result, failed, unsupported := operationResult(operationError)
		return serialize(header, omci.GetCurrentDataResponseType, &omci.GetCurrentDataResponse{
			MeBasePacket: omci.MeBasePacket{
				EntityClass:    request.EntityClass,
				EntityInstance: request.EntityInstance,
				Extended:       extended,
			},
			Result:                   result,
			AttributeMask:            request.AttributeMask &^ unsupported &^ failed,
			Attributes:               instance.Attributes,
			FailedAttributeMask:      failed,
			UnsupportedAttributeMask: unsupported,
		})

	case omci.StartSoftwareDownloadRequestType:
		request, err := layerAs[*omci.StartSoftwareDownloadRequest](packet, omci.LayerTypeStartSoftwareDownloadRequest)
		if err != nil {
			return nil, err
		}
		result := e.startSoftwareDownload(request, header.DeviceIdentifier)
		return serialize(header, omci.StartSoftwareDownloadResponseType, &omci.StartSoftwareDownloadResponse{
			MeBasePacket: omci.MeBasePacket{
				EntityClass: request.EntityClass, EntityInstance: request.EntityInstance,
				Extended: extended,
			},
			Result: result, WindowSize: request.WindowSize,
			NumberOfInstances: request.NumberOfCircuitPacks,
			MeResults:         softwareResults(request.CircuitPacks, result),
		})

	case omci.DownloadSectionRequestType, omci.DownloadSectionRequestWithResponseType:
		request, err := layerAs[*omci.DownloadSectionRequest](packet, omci.LayerTypeDownloadSectionRequest)
		if err != nil {
			return nil, err
		}
		result := e.downloadSoftwareSection(request, header.DeviceIdentifier)
		if header.MessageType == omci.DownloadSectionRequestType {
			if result != me.Success {
				return nil, fmt.Errorf("download section %d rejected: %s", request.SectionNumber, result)
			}
			return nil, nil
		}
		return serialize(header, omci.DownloadSectionResponseType, &omci.DownloadSectionResponse{
			MeBasePacket: omci.MeBasePacket{
				EntityClass: request.EntityClass, EntityInstance: request.EntityInstance,
				Extended: extended,
			},
			Result: result, SectionNumber: request.SectionNumber,
		})

	case omci.EndSoftwareDownloadRequestType:
		request, err := layerAs[*omci.EndSoftwareDownloadRequest](packet, omci.LayerTypeEndSoftwareDownloadRequest)
		if err != nil {
			return nil, err
		}
		result := e.endSoftwareDownload(request, header.DeviceIdentifier)
		return serialize(header, omci.EndSoftwareDownloadResponseType, &omci.EndSoftwareDownloadResponse{
			MeBasePacket: omci.MeBasePacket{
				EntityClass: request.EntityClass, EntityInstance: request.EntityInstance,
				Extended: extended,
			},
			Result: result, NumberOfInstances: request.NumberOfInstances,
			MeResults: softwareResults(request.ImageInstances, result),
		})

	case omci.ActivateSoftwareRequestType:
		request, err := layerAs[*omci.ActivateSoftwareRequest](packet, omci.LayerTypeActivateSoftwareRequest)
		if err != nil {
			return nil, err
		}
		result := e.activateSoftware(request)
		return serialize(header, omci.ActivateSoftwareResponseType, &omci.ActivateSoftwareResponse{
			MeBasePacket: omci.MeBasePacket{
				EntityClass: request.EntityClass, EntityInstance: request.EntityInstance,
				Extended: extended,
			},
			Result: result,
		})

	case omci.CommitSoftwareRequestType:
		request, err := layerAs[*omci.CommitSoftwareRequest](packet, omci.LayerTypeCommitSoftwareRequest)
		if err != nil {
			return nil, err
		}
		result := e.commitSoftware(request)
		return serialize(header, omci.CommitSoftwareResponseType, &omci.CommitSoftwareResponse{
			MeBasePacket: omci.MeBasePacket{
				EntityClass: request.EntityClass, EntityInstance: request.EntityInstance,
				Extended: extended,
			},
			Result: result,
		})

	case omci.TestRequestType:
		request, err := layerAs[*omci.OpticalLineSupervisionTestRequest](packet, omci.LayerTypeTestRequest)
		if err != nil {
			return nil, err
		}
		result := e.opticalLineSupervision(header, request)
		return serialize(header, omci.TestResponseType, &omci.TestResponse{
			MeBasePacket: omci.MeBasePacket{
				EntityClass:    request.EntityClass,
				EntityInstance: request.EntityInstance,
				Extended:       extended,
			},
			Result: result,
		})

	case omci.SynchronizeTimeRequestType:
		request, err := layerAs[*omci.SynchronizeTimeRequest](packet, omci.LayerTypeSynchronizeTimeRequest)
		if err != nil {
			return nil, err
		}
		result := e.synchronizeTime(request.Year, request.Month, request.Day,
			request.Hour, request.Minute, request.Second)
		return serialize(header, omci.SynchronizeTimeResponseType, &omci.SynchronizeTimeResponse{
			MeBasePacket: omci.MeBasePacket{
				EntityClass:    request.EntityClass,
				EntityInstance: request.EntityInstance,
				Extended:       extended,
			},
			Result: result,
		})

	case omci.RebootRequestType:
		request, err := layerAs[*omci.RebootRequest](packet, omci.LayerTypeRebootRequest)
		if err != nil {
			return nil, err
		}
		result := me.NotSupported
		if e.controller != nil {
			if err := e.controller.Reboot(request.RebootCondition); err != nil {
				result = me.ProcessingError
			} else {
				result = me.Success
			}
		}
		return serialize(header, omci.RebootResponseType, &omci.RebootResponse{
			MeBasePacket: omci.MeBasePacket{
				EntityClass:    request.EntityClass,
				EntityInstance: request.EntityInstance,
				Extended:       extended,
			},
			Result: result,
		})
	default:
		if len(header.LayerPayload()) >= 4 && resultBearingRequest(header.MessageType) {
			classID := me.ClassID(binary.BigEndian.Uint16(header.LayerPayload()))
			entityID := binary.BigEndian.Uint16(header.LayerPayload()[2:])
			return serializeRawResult(header, classID, entityID, me.NotSupported)
		}
		return nil, fmt.Errorf("unsupported OMCI request %s", header.MessageType)
	}
}

func (e *Engine) opticalLineSupervision(header *omci.OMCI,
	request *omci.OpticalLineSupervisionTestRequest) me.Results {
	if request.EntityClass != me.AniGClassID {
		return me.UnknownEntity
	}
	if !e.mib.Exists(mib.Key{ClassID: request.EntityClass, EntityID: request.EntityInstance}) {
		return me.UnknownInstance
	}
	if request.SelectTest != 7 || request.GeneralPurposeBuffer != 0 ||
		request.VendorSpecificParameters != 0 {
		return me.ParameterError
	}
	if e.controller == nil {
		return me.NotSupported
	}
	diagnostics, err := e.controller.OpticalLineSupervision()
	if err != nil {
		return me.ProcessingError
	}
	frame, err := serializeAutonomous(header.DeviceIdentifier, header.TransactionID,
		omci.TestResultType, &omci.OpticalLineSupervisionTestResult{
			MeBasePacket: omci.MeBasePacket{
				EntityClass:    request.EntityClass,
				EntityInstance: request.EntityInstance,
				Extended:       header.DeviceIdentifier == omci.ExtendedIdent,
			},
			PowerFeedVoltageType:     1,
			PowerFeedVoltage:         diagnostics.PowerFeedVoltage,
			ReceivedOpticalPowerType: 3,
			ReceivedOpticalPower:     diagnostics.ReceivedOpticalPower,
			MeanOpticalLaunchType:    5,
			MeanOpticalLaunch:        diagnostics.MeanOpticalLaunch,
			LaserBiasCurrentType:     9,
			LaserBiasCurrent:         diagnostics.LaserBiasCurrent,
			TemperatureType:          12,
			Temperature:              diagnostics.Temperature,
		})
	if err != nil {
		return me.ProcessingError
	}
	e.pending = append(e.pending, frame)
	return me.Success
}

func (e *Engine) handleRawSpecial(header *omci.OMCI, frame []byte) ([]byte, bool, error) {
	if header.MessageType == omci.TestRequestType &&
		header.DeviceIdentifier == omci.ExtendedIdent {
		if len(frame) < 8 {
			return nil, false, nil
		}
		classID := me.ClassID(binary.BigEndian.Uint16(frame[4:6]))
		entityID := binary.BigEndian.Uint16(frame[6:8])
		if len(frame) < 15 {
			response, err := serializeRawResult(header, classID, entityID, me.ProcessingError)
			return response, true, err
		}
		if binary.BigEndian.Uint16(frame[8:10]) != 5 {
			response, err := serializeRawResult(header, classID, entityID, me.ParameterError)
			return response, true, err
		}
		request := &omci.OpticalLineSupervisionTestRequest{
			MeBasePacket: omci.MeBasePacket{
				EntityClass: classID, EntityInstance: entityID, Extended: true,
			},
			SelectTest:               frame[10],
			GeneralPurposeBuffer:     binary.BigEndian.Uint16(frame[11:13]),
			VendorSpecificParameters: binary.BigEndian.Uint16(frame[13:15]),
		}
		result := e.opticalLineSupervision(header, request)
		response, err := serializeRawResult(header, classID, entityID, result)
		return response, true, err
	}
	if header.MessageType != omci.SynchronizeTimeRequestType || len(frame) < 8 {
		return nil, false, nil
	}
	classID := me.ClassID(binary.BigEndian.Uint16(frame[4:6]))
	entityID := binary.BigEndian.Uint16(frame[6:8])
	if classID != me.OnuGClassID {
		response, err := serializeRawResult(header, classID, entityID, me.UnknownEntity)
		return response, true, err
	}
	if entityID != 0 {
		response, err := serializeRawResult(header, classID, entityID, me.UnknownInstance)
		return response, true, err
	}
	offset := 8
	if header.DeviceIdentifier == omci.ExtendedIdent {
		offset = 10
	}
	if len(frame) < offset+7 {
		response, err := serializeRawResult(header, classID, entityID, me.ProcessingError)
		return response, true, err
	}
	result := e.synchronizeTime(binary.BigEndian.Uint16(frame[offset:]), frame[offset+2],
		frame[offset+3], frame[offset+4], frame[offset+5], frame[offset+6])
	response, err := serializeRawResult(header, classID, entityID, result)
	return response, true, err
}

func (e *Engine) synchronizeTime(year uint16, month, day, hour, minute, second uint8) me.Results {
	if e.controller == nil {
		return me.NotSupported
	}
	requested := time.Date(int(year), time.Month(month), int(day), int(hour), int(minute), int(second), 0, time.UTC)
	if requested.Year() != int(year) || requested.Month() != time.Month(month) ||
		requested.Day() != int(day) || requested.Hour() != int(hour) ||
		requested.Minute() != int(minute) || requested.Second() != int(second) {
		return me.ParameterError
	}
	if err := e.controller.SynchronizeTime(requested); err != nil {
		return me.ProcessingError
	}
	return me.Success
}

func buildUpload(snapshot []mib.Instance, device omci.DeviceIdent) ([]uploadCommand, error) {
	entities := make([]me.ManagedEntity, 0, len(snapshot))
	for _, instance := range snapshot {
		if instance.ClassID == me.ManagedEntityMeClassID || instance.ClassID == me.AttributeMeClassID {
			continue
		}
		definition, omciErr := me.LoadManagedEntityDefinition(instance.ClassID, me.ParamData{EntityID: instance.EntityID})
		if omciErr.StatusCode() != me.Success {
			return nil, fmt.Errorf("load MIB upload definition %v: %w", instance.ClassID, omciErr.GetError())
		}

		limit := omci.MaxAttributeMibUploadNextBaselineLength
		if device == omci.ExtendedIdent {
			limit = omci.MaxManagedEntityMibUploadNextExtendedLength
		}
		attributes := make(me.AttributeValueMap)
		used := 0
		flush := func() error {
			entity, entityError := me.LoadManagedEntityDefinition(instance.ClassID, me.ParamData{
				EntityID:   instance.EntityID,
				Attributes: attributes,
			})
			if entityError.StatusCode() != me.Success {
				return entityError.GetError()
			}
			entities = append(entities, *entity)
			attributes = make(me.AttributeValueMap)
			used = 0
			return nil
		}

		for _, index := range me.GetAttributeDefinitionMapKeys(definition.GetAttributeDefinitions()) {
			if index == 0 {
				continue
			}
			attribute := definition.GetAttributeDefinitions()[index]
			value, present := instance.Attributes[attribute.GetName()]
			if !present || !me.SupportsAttributeAccess(attribute, me.Read) ||
				attribute.IsTableAttribute() || attribute.IsCounter() {
				continue
			}
			size := attribute.GetSize()
			if size > limit {
				continue
			}
			if used > 0 && used+size > limit {
				if err := flush(); err != nil {
					return nil, err
				}
			}
			attributes[attribute.GetName()] = value
			used += size
		}
		if len(attributes) != 0 || used == 0 {
			if err := flush(); err != nil {
				return nil, err
			}
		}
	}

	commands := make([]uploadCommand, 0, len(entities))
	if device != omci.ExtendedIdent {
		for _, entity := range entities {
			commands = append(commands, uploadCommand{entity})
		}
		return commands, nil
	}

	limit := omci.MaxManagedEntityMibUploadNextExtendedLength
	var current uploadCommand
	used := 0
	for _, entity := range entities {
		buffer := gopacket.NewSerializeBuffer()
		if err := entity.SerializeTo(buffer, byte(omci.MibUploadNextResponseType), limit,
			gopacket.SerializeOptions{FixLengths: true}); err != nil {
			return nil, fmt.Errorf("size extended MIB upload ME %v/%#x: %w",
				entity.GetClassID(), entity.GetEntityID(), err)
		}
		cost := 2 + len(buffer.Bytes())
		if len(current) != 0 && used+cost > limit {
			commands = append(commands, current)
			current = nil
			used = 0
		}
		current = append(current, entity)
		used += cost
	}
	if len(current) != 0 {
		commands = append(commands, current)
	}
	return commands, nil
}

func (e *Engine) prepareTableGet(instance *mib.Instance, requestedMask uint16,
	device omci.DeviceIdent) error {
	definition, omciErr := me.LoadManagedEntityDefinition(instance.ClassID, me.ParamData{EntityID: instance.EntityID})
	if omciErr.StatusCode() != me.Success {
		return omciErr.GetError()
	}
	for index, attribute := range definition.GetAttributeDefinitions() {
		if index == 0 || requestedMask&attribute.Mask == 0 || !attribute.IsTableAttribute() {
			continue
		}
		value, present := instance.Attributes[attribute.GetName()]
		if !present {
			continue
		}
		var rows []byte
		switch typed := value.(type) {
		case me.TableRows:
			rows = typed.Rows
		case []byte:
			rows = typed
		case uint32:
			continue
		default:
			return fmt.Errorf("table attribute %s has unsupported value type %T", attribute.GetName(), value)
		}
		e.tables[tableKey{
			device:   device,
			classID:  instance.ClassID,
			entityID: instance.EntityID,
			mask:     attribute.Mask,
		}] = append([]byte(nil), rows...)
		instance.Attributes[attribute.GetName()] = uint32(len(rows))
	}
	return nil
}

func (e *Engine) decodeFailureResponse(header *omci.OMCI, frame []byte, decodeErr error) ([]byte, bool, error) {
	if len(frame) < 8 || byte(header.MessageType)&me.AR == 0 || !resultBearingRequest(header.MessageType) {
		return nil, false, nil
	}
	result := me.ProcessingError
	var omciError me.OmciErrors
	if errors.As(decodeErr, &omciError) && omciError.StatusCode() != me.Success {
		result = omciError.StatusCode()
	}
	classID := me.ClassID(binary.BigEndian.Uint16(frame[4:6]))
	entityID := binary.BigEndian.Uint16(frame[6:8])
	definition, definitionError := me.LoadManagedEntityDefinition(classID, me.ParamData{EntityID: entityID})
	classSupport := definition.GetManagedEntityDefinition().GetClassSupport()
	if definitionError.StatusCode() != me.Success ||
		classSupport == me.UnsupportedManagedEntity ||
		classSupport == me.UnsupportedVendorSpecificManagedEntity {
		result = me.UnknownEntity
	} else if header.MessageType != omci.CreateRequestType &&
		!e.mib.Exists(mib.Key{ClassID: classID, EntityID: entityID}) {
		result = me.UnknownInstance
	}
	response, err := serializeRawResult(header, classID, entityID, result)
	return response, true, err
}

func resultBearingRequest(messageType omci.MessageType) bool {
	switch messageType {
	case omci.CreateRequestType,
		omci.DeleteRequestType,
		omci.SetRequestType,
		omci.GetRequestType,
		omci.MibResetRequestType,
		omci.TestRequestType,
		omci.StartSoftwareDownloadRequestType,
		omci.DownloadSectionRequestWithResponseType,
		omci.EndSoftwareDownloadRequestType,
		omci.ActivateSoftwareRequestType,
		omci.CommitSoftwareRequestType,
		omci.SynchronizeTimeRequestType,
		omci.RebootRequestType,
		omci.GetNextRequestType,
		omci.GetCurrentDataRequestType,
		omci.SetTableRequestType:
		return true
	default:
		return false
	}
}

type rawResultLayer struct {
	classID  me.ClassID
	entityID uint16
	result   me.Results
	extended bool
}

type emptyMibUploadNextLayer struct {
	classID  me.ClassID
	entityID uint16
	extended bool
}

func (l *emptyMibUploadNextLayer) LayerType() gopacket.LayerType { return gopacket.LayerTypePayload }
func (l *emptyMibUploadNextLayer) LayerContents() []byte         { return nil }
func (l *emptyMibUploadNextLayer) LayerPayload() []byte          { return nil }

func (l *emptyMibUploadNextLayer) SerializeTo(buffer gopacket.SerializeBuffer,
	_ gopacket.SerializeOptions) error {
	length := 4
	if l.extended {
		// Extended MIB upload next signals an invalid command with a zero
		// message-contents length. Baseline padding is supplied by the OMCI layer.
		length = 6
	}
	encoded, err := buffer.AppendBytes(length)
	if err != nil {
		return err
	}
	binary.BigEndian.PutUint16(encoded, uint16(l.classID))
	binary.BigEndian.PutUint16(encoded[2:], l.entityID)
	return nil
}

func (l *rawResultLayer) LayerType() gopacket.LayerType { return gopacket.LayerTypePayload }
func (l *rawResultLayer) LayerContents() []byte         { return nil }
func (l *rawResultLayer) LayerPayload() []byte          { return nil }

func (l *rawResultLayer) SerializeTo(buffer gopacket.SerializeBuffer, _ gopacket.SerializeOptions) error {
	length := 5
	resultOffset := 4
	if l.extended {
		length = 7
		resultOffset = 6
	}
	encoded, err := buffer.AppendBytes(length)
	if err != nil {
		return err
	}
	binary.BigEndian.PutUint16(encoded, uint16(l.classID))
	binary.BigEndian.PutUint16(encoded[2:], l.entityID)
	if l.extended {
		binary.BigEndian.PutUint16(encoded[4:], 1)
	}
	encoded[resultOffset] = byte(l.result)
	return nil
}

func serializeRawResult(header *omci.OMCI, classID me.ClassID, entityID uint16,
	result me.Results) ([]byte, error) {
	responseType := omci.MessageType(byte(header.MessageType)&^me.AR | me.AK)
	responseHeader := &omci.OMCI{
		TransactionID:    header.TransactionID,
		MessageType:      responseType,
		DeviceIdentifier: header.DeviceIdentifier,
	}
	layer := &rawResultLayer{
		classID: classID, entityID: entityID, result: result,
		extended: header.DeviceIdentifier == omci.ExtendedIdent,
	}
	buffer := gopacket.NewSerializeBuffer()
	if err := gopacket.SerializeLayers(buffer,
		gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: false},
		responseHeader, layer); err != nil {
		return nil, fmt.Errorf("serialize raw OMCI error response: %w", err)
	}
	return append([]byte(nil), buffer.Bytes()...), nil
}

func serializeEmptyMibUploadNext(header *omci.OMCI, classID me.ClassID,
	entityID uint16) ([]byte, error) {
	return serialize(header, omci.MibUploadNextResponseType, &emptyMibUploadNextLayer{
		classID: classID, entityID: entityID,
		extended: header.DeviceIdentifier == omci.ExtendedIdent,
	})
}

func serialize(header *omci.OMCI, responseType omci.MessageType, response gopacket.SerializableLayer) ([]byte, error) {
	responseHeader := &omci.OMCI{
		TransactionID:    header.TransactionID,
		MessageType:      responseType,
		DeviceIdentifier: header.DeviceIdentifier,
	}
	buffer := gopacket.NewSerializeBuffer()
	options := gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: false}
	if err := gopacket.SerializeLayers(buffer, options, responseHeader, response); err != nil {
		return nil, fmt.Errorf("serialize %s: %w", responseType, err)
	}
	return append([]byte(nil), buffer.Bytes()...), nil
}

func layerAs[T any](packet gopacket.Packet, layerType gopacket.LayerType) (T, error) {
	var zero T
	layer := packet.Layer(layerType)
	if layer == nil {
		return zero, fmt.Errorf("OMCI layer %s is missing", layerType)
	}
	value, ok := layer.(T)
	if !ok {
		return zero, fmt.Errorf("unexpected OMCI layer type %T for %s", layer, layerType)
	}
	return value, nil
}

func operationResult(err error) (me.Results, uint16, uint16) {
	if err == nil {
		return me.Success, 0, 0
	}
	var result *mib.ResultError
	if errors.As(err, &result) {
		return result.Result, result.FailedMask, result.UnsupportedMask
	}
	return me.ProcessingError, 0, 0
}

func (e *Engine) replayTransaction(header *omci.OMCI) ([]byte, bool) {
	if byte(header.MessageType)&me.AR == 0 {
		return nil, false
	}
	replay := e.transactions[transactionPriority(header)]
	if !replay.valid || replay.transactionID != header.TransactionID {
		return nil, false
	}
	if header.MessageType == omci.MibUploadNextRequestType && replay.refreshUpload {
		now := e.now()
		session, present := e.upload[header.DeviceIdentifier]
		if !present || !now.Before(session.expires) {
			delete(e.upload, header.DeviceIdentifier)
			return nil, false
		}
		session.expires = now.Add(mibUploadTimeout)
		e.upload[header.DeviceIdentifier] = session
	}
	if header.MessageType == omci.MibUploadRequestType {
		if session, present := e.upload[header.DeviceIdentifier]; present {
			session.expires = e.now().Add(mibUploadTimeout)
			e.upload[header.DeviceIdentifier] = session
		}
	}
	return append([]byte(nil), replay.response...), true
}

func (e *Engine) rememberTransaction(header *omci.OMCI, response []byte) {
	if byte(header.MessageType)&me.AR == 0 {
		return
	}
	e.transactions[transactionPriority(header)] = transactionReplay{
		valid: true, transactionID: header.TransactionID,
		response: append([]byte(nil), response...),
		refreshUpload: header.MessageType == omci.MibUploadNextRequestType &&
			mibUploadNextHasContents(response, header.DeviceIdentifier),
	}
}

func mibUploadNextHasContents(response []byte, device omci.DeviceIdent) bool {
	if device == omci.ExtendedIdent {
		return len(response) >= 10 && binary.BigEndian.Uint16(response[8:10]) != 0
	}
	if len(response) < 14 {
		return false
	}
	for _, value := range response[8:14] {
		if value != 0 {
			return true
		}
	}
	return false
}

func transactionPriority(header *omci.OMCI) transactionChannel {
	if header.DeviceIdentifier == omci.ExtendedIdent {
		return extendedPriority
	}
	if header.TransactionID&0x8000 != 0 {
		return baselineHighPriority
	}
	return baselineLowPriority
}

func validateFrame(frame []byte) error {
	if len(frame) < 4 || len(frame) > omci.MaxExtendedLength {
		return fmt.Errorf("invalid OMCI frame length %d", len(frame))
	}
	switch omci.DeviceIdent(frame[3]) {
	case omci.BaselineIdent:
		// OMCC adapters may remove the MIC or the complete eight-byte trailer
		// after validating it. omci-lib-go emits the MIC-stripped form.
		if len(frame) != omci.MaxBaselineLength && len(frame) != omci.MaxBaselineLength-4 &&
			len(frame) != omci.MaxBaselineLength-8 {
			return fmt.Errorf("invalid baseline OMCI frame length %d", len(frame))
		}
	case omci.ExtendedIdent:
		if len(frame) < 10 {
			return fmt.Errorf("invalid extended OMCI frame length %d", len(frame))
		}
		payloadEnd := 10 + int(binary.BigEndian.Uint16(frame[8:10]))
		// The four-byte MIC may likewise be consumed by the OMCC adapter.
		if payloadEnd > omci.MaxExtendedLength-4 ||
			(len(frame) != payloadEnd && len(frame) != payloadEnd+4) {
			return fmt.Errorf("extended OMCI content length %d does not match frame length %d",
				payloadEnd-10, len(frame))
		}
	default:
		return fmt.Errorf("unsupported OMCI device identifier %#x", frame[3])
	}
	return nil
}
