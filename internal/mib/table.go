// SPDX-License-Identifier: Apache-2.0

package mib

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/bits"
	"sort"

	me "github.com/opencord/omci-lib-go/v2/generated"
	"github.com/xg2010g/airoha-omci/internal/vlan"
)

const (
	extendedVLANRowSize         = 16
	enhancedExtendedVLANRowSize = 28
)

var extendedVLANDefaultRows = []byte{
	0xf8, 0x00, 0x00, 0x00, 0xf8, 0x00, 0x00, 0x00, 0x00, 0x0f, 0x00, 0x00, 0x00, 0x0f, 0x00, 0x00,
	0xf8, 0x00, 0x00, 0x00, 0xe8, 0x00, 0x00, 0x00, 0x00, 0x0f, 0x00, 0x00, 0x00, 0x0f, 0x00, 0x00,
	0xe8, 0x00, 0x00, 0x00, 0xe8, 0x00, 0x00, 0x00, 0x00, 0x0f, 0x00, 0x00, 0x00, 0x0f, 0x00, 0x00,
}

func initializeCreatedInstance(instance *Instance, extendedVLANTableSize uint16) {
	if instance.ClassID != me.ExtendedVlanTaggingOperationConfigurationDataClassID {
		return
	}
	instance.Attributes[me.ExtendedVlanTaggingOperationConfigurationData_ReceivedFrameVlanTaggingOperationTableMaxSize] = extendedVLANTableSize
	instance.Attributes[me.ExtendedVlanTaggingOperationConfigurationData_ReceivedFrameVlanTaggingOperationTable] = me.TableRows{
		NumRows: 3,
		Rows:    append([]byte(nil), extendedVLANDefaultRows...),
	}
	instance.Attributes[me.ExtendedVlanTaggingOperationConfigurationData_EnhancedReceivedFrameClassificationAndProcessingTable] = me.TableRows{}
}

// SetTable applies one complete extended-message SetTable request. The
// candidate table and the full MIB snapshot reach the platform before either
// the table or MIB data sync is committed.
func (s *Store) SetTable(key Key, mask uint16, attributes me.AttributeValueMap) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	current, exists := s.current[key]
	if !exists {
		return unknownKeyError(key)
	}
	entity, result := loadDefinition(key.ClassID, key.EntityID, current.Attributes)
	if result != nil {
		return result
	}
	if !me.SupportsMsgType(entity, me.SetTable) {
		return &ResultError{Result: me.NotSupported}
	}
	if bits.OnesCount16(mask) != 1 {
		return &ResultError{Result: me.ParameterError, Cause: fmt.Errorf("SetTable mask %#x does not select one attribute", mask)}
	}

	var definition *me.AttributeDefinition
	for index, candidate := range entity.GetAttributeDefinitions() {
		if index != 0 && candidate.Mask == mask {
			copy := candidate
			definition = &copy
			break
		}
	}
	if definition == nil || !definition.IsTableAttribute() ||
		!me.SupportsAttributeAccess(*definition, me.Write) {
		return &ResultError{Result: me.AttributeFailure, FailedMask: mask}
	}
	if len(attributes) != 1 && !(len(attributes) == 2 && attributes[me.ManagedEntityID] != nil) {
		return &ResultError{Result: me.ParameterError, Cause: fmt.Errorf("SetTable requires exactly one table attribute")}
	}
	value, present := attributes[definition.GetName()]
	if !present {
		return &ResultError{Result: me.AttributeFailure, FailedMask: mask}
	}
	updates, ok := value.(me.TableRows)
	if !ok || updates.NumRows < 0 || len(updates.Rows) != updates.NumRows*definition.GetSize() {
		return &ResultError{Result: me.ParameterError, Cause: fmt.Errorf("invalid %s row data", definition.GetName())}
	}

	existing, err := tableRows(current.Attributes[definition.GetName()], definition.GetSize())
	if err != nil {
		return &ResultError{Result: me.ProcessingError, Cause: err}
	}
	updated, err := s.applyTableRows(key.ClassID, definition.GetName(), existing, updates)
	if err != nil {
		return err
	}

	next := cloneInstance(current)
	next.Attributes[definition.GetName()] = updated
	normalized, err := normalize(next)
	if err != nil {
		return err
	}
	proposed := cloneInstances(s.current)
	proposed[key] = normalized
	return s.commitLocked(OperationSetTable, &current, &normalized, proposed, s.nextDataSyncLocked())
}

func (s *Store) applyTableRows(classID me.ClassID, name string, existing, updates me.TableRows) (me.TableRows, error) {
	if classID != me.ExtendedVlanTaggingOperationConfigurationDataClassID {
		return me.TableRows{}, &ResultError{Result: me.NotSupported,
			Cause: fmt.Errorf("SetTable policy for class %#x attribute %s is not implemented", classID, name)}
	}

	switch name {
	case me.ExtendedVlanTaggingOperationConfigurationData_ReceivedFrameVlanTaggingOperationTable:
		for offset := 0; offset < len(updates.Rows); offset += extendedVLANRowSize {
			row := updates.Rows[offset : offset+extendedVLANRowSize]
			if allBytes(row[8:], 0xff) {
				continue
			}
			if _, err := vlan.ParseRow(row, false); err != nil {
				return me.TableRows{}, &ResultError{Result: me.ParameterError, Cause: err}
			}
		}
		rows := applyKeyedRows(existing.Rows, updates.Rows, extendedVLANRowSize, 8,
			func(row []byte) bool { return allBytes(row[8:], 0xff) })
		if len(rows)/extendedVLANRowSize > int(s.extendedVLANTableSize) {
			return me.TableRows{}, tableCapacityError(len(rows)/extendedVLANRowSize, s.extendedVLANTableSize)
		}
		return me.TableRows{NumRows: len(rows) / extendedVLANRowSize, Rows: rows}, nil

	case me.ExtendedVlanTaggingOperationConfigurationData_EnhancedReceivedFrameClassificationAndProcessingTable:
		rows, err := applyEnhancedVLANRows(existing.Rows, updates.Rows)
		if err != nil {
			return me.TableRows{}, err
		}
		if len(rows)/enhancedExtendedVLANRowSize > int(s.extendedVLANTableSize) {
			return me.TableRows{}, tableCapacityError(len(rows)/enhancedExtendedVLANRowSize, s.extendedVLANTableSize)
		}
		return me.TableRows{NumRows: len(rows) / enhancedExtendedVLANRowSize, Rows: rows}, nil
	default:
		return me.TableRows{}, &ResultError{Result: me.NotSupported,
			Cause: fmt.Errorf("SetTable policy for class %#x attribute %s is not implemented", classID, name)}
	}
}

func tableRows(value interface{}, rowSize int) (me.TableRows, error) {
	if value == nil {
		return me.TableRows{}, nil
	}
	rows, ok := value.(me.TableRows)
	if !ok || rows.NumRows < 0 || len(rows.Rows) != rows.NumRows*rowSize {
		return me.TableRows{}, fmt.Errorf("committed table has invalid row data")
	}
	return me.TableRows{NumRows: rows.NumRows, Rows: append([]byte(nil), rows.Rows...)}, nil
}

func applyKeyedRows(existing, updates []byte, rowSize, keySize int, deleted func([]byte) bool) []byte {
	rows := make(map[string][]byte, len(existing)/rowSize+len(updates)/rowSize)
	for offset := 0; offset < len(existing); offset += rowSize {
		row := existing[offset : offset+rowSize]
		rows[string(row[:keySize])] = append([]byte(nil), row...)
	}
	for offset := 0; offset < len(updates); offset += rowSize {
		row := updates[offset : offset+rowSize]
		key := string(row[:keySize])
		if deleted(row) {
			delete(rows, key)
		} else {
			rows[key] = append([]byte(nil), row...)
		}
	}
	keys := make([]string, 0, len(rows))
	for key := range rows {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]byte, 0, len(rows)*rowSize)
	for _, key := range keys {
		result = append(result, rows[key]...)
	}
	return result
}

func applyEnhancedVLANRows(existing, updates []byte) ([]byte, error) {
	rows := make(map[uint16][]byte, len(existing)/enhancedExtendedVLANRowSize)
	for offset := 0; offset < len(existing); offset += enhancedExtendedVLANRowSize {
		row := append([]byte(nil), existing[offset:offset+enhancedExtendedVLANRowSize]...)
		row[0] &= 0x3f
		rows[binary.BigEndian.Uint16(row[2:4])] = row
	}
	for offset := 0; offset < len(updates); offset += enhancedExtendedVLANRowSize {
		row := updates[offset : offset+enhancedExtendedVLANRowSize]
		control := row[0] >> 6
		key := binary.BigEndian.Uint16(row[2:4])
		switch control {
		case 1:
			if direction := (row[0] >> 4) & 0x03; direction == 3 {
				return nil, &ResultError{Result: me.ParameterError,
					Cause: fmt.Errorf("enhanced VLAN row %#x has reserved direction", key)}
			}
			stored := append([]byte(nil), row...)
			stored[0] &= 0x3f
			if _, err := vlan.ParseRow(stored, true); err != nil {
				return nil, &ResultError{Result: me.ParameterError, Cause: err}
			}
			rows[key] = stored
		case 2:
			delete(rows, key)
		case 3:
			clear(rows)
		default:
			return nil, &ResultError{Result: me.ParameterError,
				Cause: fmt.Errorf("enhanced VLAN row %#x has reserved set control", key)}
		}
	}
	keys := make([]int, 0, len(rows))
	for key := range rows {
		keys = append(keys, int(key))
	}
	sort.Ints(keys)
	result := make([]byte, 0, len(rows)*enhancedExtendedVLANRowSize)
	for _, key := range keys {
		result = append(result, rows[uint16(key)]...)
	}
	return result, nil
}

func allBytes(value []byte, expected byte) bool {
	return len(value) != 0 && bytes.Count(value, []byte{expected}) == len(value)
}

func tableCapacityError(rows int, capacity uint16) error {
	return &ResultError{Result: me.ProcessingError,
		Cause: fmt.Errorf("table has %d rows, maximum is %d", rows, capacity)}
}
