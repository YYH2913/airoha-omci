// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"fmt"

	"github.com/google/gopacket"
	omci "github.com/opencord/omci-lib-go/v2"
	me "github.com/opencord/omci-lib-go/v2/generated"
	"github.com/xg2010g/airoha-omci/internal/mib"
)

const baselineAVCPayloadLimit = 30

// NotifyAlarm updates the alarm audit table and returns an autonomous alarm
// notification when the bitmap changed. Alarm sequence numbers are shared by
// all managed entities, range from 1 through 255 and wrap back to 1.
func (e *Engine) NotifyAlarm(key mib.Key, bitmap [28]byte,
	device omci.DeviceIdent) ([]byte, bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.notifyAlarmLocked(key, bitmap, device)
}

// NotifyAlarmBit changes one alarm condition without overwriting other active
// alarms for the same managed entity.
func (e *Engine) NotifyAlarmBit(key mib.Key, alarm uint8, active bool,
	device omci.DeviceIdent) ([]byte, bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if alarm >= omci.AlarmBitmapSize {
		return nil, false, fmt.Errorf("alarm bit %d is outside the 224-bit bitmap", alarm)
	}
	bitmap := e.alarms[key]
	octet := alarm / 8
	bit := uint(7 - alarm%8)
	if active {
		bitmap[octet] |= 1 << bit
	} else {
		bitmap[octet] &^= 1 << bit
	}
	return e.notifyAlarmLocked(key, bitmap, device)
}

func (e *Engine) notifyAlarmLocked(key mib.Key, bitmap [28]byte,
	device omci.DeviceIdent) ([]byte, bool, error) {

	if err := validateDeviceIdentifier(device); err != nil {
		return nil, false, err
	}
	device = e.notificationDeviceLocked(device)
	if !e.mib.Exists(key) {
		return nil, false, fmt.Errorf("alarm target %v/%#x does not exist", key.ClassID, key.EntityID)
	}
	entity, omciErr := me.LoadManagedEntityDefinition(key.ClassID, me.ParamData{EntityID: key.EntityID})
	if omciErr.StatusCode() != me.Success {
		return nil, false, omciErr.GetError()
	}
	if err := validateAlarmBitmap(entity.GetAlarmMap(), bitmap); err != nil {
		return nil, false, fmt.Errorf("alarm target %v/%#x: %w", key.ClassID, key.EntityID, err)
	}
	previous, found := e.alarms[key]
	if found && previous == bitmap {
		return nil, false, nil
	}
	if !found && bitmap == ([28]byte{}) {
		return nil, false, nil
	}

	if bitmap == ([28]byte{}) {
		delete(e.alarms, key)
	} else {
		e.alarms[key] = bitmap
	}
	if e.arcEnabledLocked(key) {
		if bitmap == ([28]byte{}) {
			if _, exists := e.arcFreeSince[key]; !exists {
				e.arcFreeSince[key] = e.now()
			}
		} else {
			delete(e.arcFreeSince, key)
		}
		frames, err := e.pollARCKeyLocked(key, device)
		if err != nil {
			return nil, false, err
		}
		if len(frames) == 0 {
			return nil, false, nil
		}
		return frames[0], true, nil
	}

	previousSequence := e.alarmSequence
	sequence := e.nextAlarmSequenceLocked()
	frame, err := serializeAutonomous(device, 0, omci.AlarmNotificationType,
		&omci.AlarmNotificationMsg{
			MeBasePacket: omci.MeBasePacket{
				EntityClass:    key.ClassID,
				EntityInstance: key.EntityID,
				Extended:       device == omci.ExtendedIdent,
			},
			AlarmBitmap:         bitmap,
			AlarmSequenceNumber: sequence,
		})
	if err != nil {
		e.alarmSequence = previousSequence
		if found {
			e.alarms[key] = previous
		} else {
			delete(e.alarms, key)
		}
		return nil, false, err
	}
	return frame, true, nil
}

func (e *Engine) nextAlarmSequenceLocked() uint8 {
	if e.alarmSequence == 0 || e.alarmSequence == 255 {
		e.alarmSequence = 1
	} else {
		e.alarmSequence++
	}
	return e.alarmSequence
}

// NotifyAttributeChange commits ONU-originated state and returns one or more
// AVC frames containing the changed attributes that are marked AVC-capable in
// the managed-entity definition. Baseline messages are split at attribute
// boundaries when needed.
func (e *Engine) NotifyAttributeChange(key mib.Key, attributes me.AttributeValueMap,
	device omci.DeviceIdent) ([][]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.notifyAttributeChangeLocked(key, attributes, device)
}

func (e *Engine) notifyAttributeChangeLocked(key mib.Key, attributes me.AttributeValueMap,
	device omci.DeviceIdent) ([][]byte, error) {
	if err := validateDeviceIdentifier(device); err != nil {
		return nil, err
	}
	device = e.notificationDeviceLocked(device)
	changed, err := e.mib.UpdateAutonomous(key, attributes)
	if err != nil {
		return nil, err
	}
	if len(changed) == 0 {
		return nil, nil
	}

	entity, omciErr := me.LoadManagedEntityDefinition(key.ClassID, me.ParamData{EntityID: key.EntityID})
	if omciErr.StatusCode() != me.Success {
		return nil, omciErr.GetError()
	}
	definitions := entity.GetAttributeDefinitions()
	limit := omci.MaxExtendedLength - 12 - 4
	if device == omci.BaselineIdent {
		limit = baselineAVCPayloadLimit
	}

	type avcAttribute struct {
		name  string
		mask  uint16
		size  int
		value interface{}
	}
	ordered := make([]avcAttribute, 0, len(changed))
	for _, index := range me.GetAttributeDefinitionMapKeys(definitions) {
		if index == 0 {
			continue
		}
		definition := definitions[index]
		value, present := changed[definition.GetName()]
		if !present || !definition.Avc {
			continue
		}
		if definition.IsTableAttribute() || definition.GetSize() <= 0 || definition.GetSize() > limit {
			return nil, fmt.Errorf("AVC attribute %s cannot fit in %v format", definition.GetName(), device)
		}
		ordered = append(ordered, avcAttribute{
			name: definition.GetName(), mask: definition.Mask,
			size: definition.GetSize(), value: value,
		})
	}
	if len(ordered) == 0 {
		return nil, nil
	}

	var frames [][]byte
	current := make(me.AttributeValueMap)
	var mask uint16
	used := 0
	flush := func() error {
		frame, serializeErr := serializeAutonomous(device, 0, omci.AttributeValueChangeType,
			&omci.AttributeValueChangeMsg{
				MeBasePacket: omci.MeBasePacket{
					EntityClass:    key.ClassID,
					EntityInstance: key.EntityID,
					Extended:       device == omci.ExtendedIdent,
				},
				AttributeMask: mask,
				Attributes:    current,
			})
		if serializeErr != nil {
			return serializeErr
		}
		frames = append(frames, frame)
		current = make(me.AttributeValueMap)
		mask = 0
		used = 0
		return nil
	}
	for _, attribute := range ordered {
		if used != 0 && used+attribute.size > limit {
			if err := flush(); err != nil {
				return nil, err
			}
		}
		current[attribute.name] = attribute.value
		mask |= attribute.mask
		used += attribute.size
	}
	if len(current) != 0 {
		if err := flush(); err != nil {
			return nil, err
		}
	}
	return frames, nil
}

// TestResult serializes an autonomous or OLT-requested test result. A zero TCI
// denotes a self-initiated test; an OLT-requested result retains the request TCI.
func (e *Engine) TestResult(transactionID uint16, key mib.Key, payload []byte,
	device omci.DeviceIdent) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := validateDeviceIdentifier(device); err != nil {
		return nil, err
	}
	device = e.notificationDeviceLocked(device)
	if !e.mib.Exists(key) {
		return nil, fmt.Errorf("test target %v/%#x does not exist", key.ClassID, key.EntityID)
	}
	entity, omciErr := me.LoadManagedEntityDefinition(key.ClassID, me.ParamData{EntityID: key.EntityID})
	if omciErr.StatusCode() != me.Success {
		return nil, omciErr.GetError()
	}
	if !me.SupportsMsgType(entity, me.Test) {
		return nil, fmt.Errorf("managed entity %v/%#x does not support Test", key.ClassID, key.EntityID)
	}
	return serializeAutonomous(device, transactionID, omci.TestResultType,
		&omci.TestResultNotification{
			MeBasePacket: omci.MeBasePacket{
				EntityClass:    key.ClassID,
				EntityInstance: key.EntityID,
				Extended:       device == omci.ExtendedIdent,
			},
			Payload: append([]byte(nil), payload...),
		})
}

func serializeAutonomous(device omci.DeviceIdent, transactionID uint16,
	messageType omci.MessageType, payload gopacket.SerializableLayer) ([]byte, error) {
	header := &omci.OMCI{
		TransactionID:    transactionID,
		MessageType:      messageType,
		DeviceIdentifier: device,
	}
	buffer := gopacket.NewSerializeBuffer()
	if err := gopacket.SerializeLayers(buffer,
		gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: false},
		header, payload); err != nil {
		return nil, fmt.Errorf("serialize %s: %w", messageType, err)
	}
	return append([]byte(nil), buffer.Bytes()...), nil
}

func validateDeviceIdentifier(device omci.DeviceIdent) error {
	if device != omci.BaselineIdent && device != omci.ExtendedIdent {
		return fmt.Errorf("unsupported OMCI device identifier %#x", byte(device))
	}
	return nil
}

func (e *Engine) notificationDeviceLocked(requested omci.DeviceIdent) omci.DeviceIdent {
	if requested == omci.ExtendedIdent && !e.extendedSeen {
		return omci.BaselineIdent
	}
	return requested
}

func validateAlarmBitmap(alarmMap me.AlarmMap, bitmap [28]byte) error {
	if len(alarmMap) == 0 {
		return fmt.Errorf("managed entity has no alarm map")
	}
	for alarm := 0; alarm < omci.AlarmBitmapSize; alarm++ {
		octet := alarm / 8
		bit := uint(7 - alarm%8)
		if bitmap[octet]&(1<<bit) == 0 {
			continue
		}
		if _, supported := alarmMap[uint8(alarm)]; !supported {
			return fmt.Errorf("alarm bit %d is not defined", alarm)
		}
	}
	return nil
}
