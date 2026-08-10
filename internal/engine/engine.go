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
)

const responseCacheSize = 64

type uploadCommand []me.ManagedEntity

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
}

type Engine struct {
	mu sync.Mutex

	mib *mib.Store

	cache       map[[sha256.Size]byte][]byte
	cacheOrder  [][sha256.Size]byte
	upload      map[omci.DeviceIdent][]uploadCommand
	tables      map[tableKey][]byte
	alarms      map[mib.Key][28]byte
	alarmUpload map[omci.DeviceIdent][]Alarm
	controller  Controller
}

func New(store *mib.Store) *Engine {
	return NewWithController(store, nil)
}

func NewWithController(store *mib.Store, controller Controller) *Engine {
	return &Engine{
		mib:         store,
		cache:       make(map[[sha256.Size]byte][]byte),
		upload:      make(map[omci.DeviceIdent][]uploadCommand),
		tables:      make(map[tableKey][]byte),
		alarms:      make(map[mib.Key][28]byte),
		alarmUpload: make(map[omci.DeviceIdent][]Alarm),
		controller:  controller,
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

// Handle processes one complete downstream OMCI frame and returns the upstream
// response. An exact retransmission is answered from a bounded cache and is
// never applied to the MIB twice.
func (e *Engine) Handle(frame []byte) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	digest := sha256.Sum256(frame)
	if response, found := e.cache[digest]; found {
		return append([]byte(nil), response...), nil
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
	if decodeError := packet.ErrorLayer(); decodeError != nil {
		if response, handled, err := e.handleRawSpecial(header, frame); handled {
			if err != nil {
				return nil, err
			}
			e.remember(digest, response)
			return append([]byte(nil), response...), nil
		}
		response, handled, err := e.decodeFailureResponse(header, frame, decodeError.Error())
		if err != nil {
			return nil, err
		}
		if !handled {
			return nil, fmt.Errorf("decode OMCI request: %w", decodeError.Error())
		}
		e.remember(digest, response)
		return append([]byte(nil), response...), nil
	}
	if byte(header.MessageType)&me.AK != 0 || byte(header.MessageType)&me.AR == 0 {
		return nil, fmt.Errorf("unexpected downstream message type %#x", byte(header.MessageType))
	}

	response, err := e.dispatch(packet, header)
	if err != nil {
		return nil, err
	}
	e.remember(digest, response)
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
		result, failed, unsupported := operationResult(e.mib.Set(mib.Key{
			ClassID:  request.EntityClass,
			EntityID: request.EntityInstance,
		}, request.Attributes))
		if result == me.Success {
			e.tables = make(map[tableKey][]byte)
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
			e.upload = make(map[omci.DeviceIdent][]uploadCommand)
			e.tables = make(map[tableKey][]byte)
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
		e.upload[header.DeviceIdentifier] = commands
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
		commands := e.upload[header.DeviceIdentifier]
		if int(request.CommandSequenceNumber) >= len(commands) {
			return nil, fmt.Errorf("MIB upload sequence %d outside snapshot of %d commands", request.CommandSequenceNumber, len(commands))
		}
		command := commands[request.CommandSequenceNumber]
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
			alarms = append(alarms, Alarm{Key: key, Bitmap: bitmap})
		}
		sort.Slice(alarms, func(i, j int) bool {
			if alarms[i].Key.ClassID == alarms[j].Key.ClassID {
				return alarms[i].Key.EntityID < alarms[j].Key.EntityID
			}
			return alarms[i].Key.ClassID < alarms[j].Key.ClassID
		})
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
		result, _, _ := operationResult(e.mib.Set(mib.Key{
			ClassID:  request.EntityClass,
			EntityID: request.EntityInstance,
		}, request.Attributes))
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

func (e *Engine) handleRawSpecial(header *omci.OMCI, frame []byte) ([]byte, bool, error) {
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
			if !present || !me.SupportsAttributeAccess(attribute, me.Read) || attribute.IsTableAttribute() {
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

func (e *Engine) remember(digest [sha256.Size]byte, response []byte) {
	if len(e.cacheOrder) == responseCacheSize {
		delete(e.cache, e.cacheOrder[0])
		e.cacheOrder = e.cacheOrder[1:]
	}
	e.cache[digest] = append([]byte(nil), response...)
	e.cacheOrder = append(e.cacheOrder, digest)
}
