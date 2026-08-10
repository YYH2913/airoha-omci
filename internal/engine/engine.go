// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"

	"github.com/google/gopacket"
	omci "github.com/opencord/omci-lib-go/v2"
	me "github.com/opencord/omci-lib-go/v2/generated"
	"github.com/xg2010g/airoha-omci/internal/mib"
)

const responseCacheSize = 64

type Engine struct {
	mu sync.Mutex

	mib *mib.Store

	cache      map[[sha256.Size]byte][]byte
	cacheOrder [][sha256.Size]byte
	upload     map[omci.DeviceIdent][]me.ManagedEntity
}

func New(store *mib.Store) *Engine {
	return &Engine{
		mib:    store,
		cache:  make(map[[sha256.Size]byte][]byte),
		upload: make(map[omci.DeviceIdent][]me.ManagedEntity),
	}
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
	if decodeError := packet.ErrorLayer(); decodeError != nil {
		return nil, fmt.Errorf("decode OMCI request: %w", decodeError.Error())
	}
	headerLayer := packet.Layer(omci.LayerTypeOMCI)
	if headerLayer == nil {
		return nil, errors.New("OMCI header is missing")
	}
	header, ok := headerLayer.(*omci.OMCI)
	if !ok {
		return nil, errors.New("invalid OMCI header layer")
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
		e.mib.Reset()
		e.upload = make(map[omci.DeviceIdent][]me.ManagedEntity)
		return serialize(header, omci.MibResetResponseType, &omci.MibResetResponse{
			MeBasePacket: omci.MeBasePacket{
				EntityClass:    request.EntityClass,
				EntityInstance: request.EntityInstance,
				Extended:       extended,
			},
			Result: me.Success,
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
		return serialize(header, omci.MibUploadNextResponseType, &omci.MibUploadNextResponse{
			MeBasePacket: omci.MeBasePacket{
				EntityClass:    request.EntityClass,
				EntityInstance: request.EntityInstance,
				Extended:       extended,
			},
			ReportedME: commands[request.CommandSequenceNumber],
		})
	default:
		return nil, fmt.Errorf("unsupported OMCI request %s", header.MessageType)
	}
}

func buildUpload(snapshot []mib.Instance, device omci.DeviceIdent) ([]me.ManagedEntity, error) {
	commands := make([]me.ManagedEntity, 0, len(snapshot))
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
			commands = append(commands, *entity)
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
	return commands, nil
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
