// SPDX-License-Identifier: Apache-2.0

package mib

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	me "github.com/opencord/omci-lib-go/v2/generated"
)

const extendedVLANEntityID = 0x101

func TestExtendedVLANCreateInitializesCapacityAndDefaults(t *testing.T) {
	store := newExtendedVLANStore(t, Options{ExtendedVLANTableSize: 96})
	instance := snapshotInstance(t, store, Key{
		ClassID:  me.ExtendedVlanTaggingOperationConfigurationDataClassID,
		EntityID: extendedVLANEntityID,
	})
	if got := instance.Attributes[me.ExtendedVlanTaggingOperationConfigurationData_ReceivedFrameVlanTaggingOperationTableMaxSize]; got != uint16(96) {
		t.Fatalf("table maximum = %#v, want 96", got)
	}
	if got := instance.Attributes[me.ExtendedVlanTaggingOperationConfigurationData_InputTpid]; got != uint16(0x88a8) {
		t.Fatalf("input TPID = %#v, want 0x88a8", got)
	}
	if got := instance.Attributes[me.ExtendedVlanTaggingOperationConfigurationData_OutputTpid]; got != uint16(0x88a8) {
		t.Fatalf("output TPID = %#v, want 0x88a8", got)
	}
	if got := instance.Attributes[me.ExtendedVlanTaggingOperationConfigurationData_DownstreamMode]; got != uint8(0) {
		t.Fatalf("downstream mode = %#v, want 0", got)
	}
	if got := instance.Attributes[me.ExtendedVlanTaggingOperationConfigurationData_EnhancedMode]; got != uint8(1) {
		t.Fatalf("enhanced mode = %#v, want requested value 1", got)
	}
	want := me.TableRows{NumRows: 3, Rows: extendedVLANDefaultRows}
	if got := instance.Attributes[me.ExtendedVlanTaggingOperationConfigurationData_ReceivedFrameVlanTaggingOperationTable]; !tableRowsEqual(got, want) {
		t.Fatalf("default rows = %#v, want %#v", got, want)
	}
	rows := want.Rows
	if binary.BigEndian.Uint32(rows[0:4]) != 15<<28|4096<<15 ||
		binary.BigEndian.Uint32(rows[4:8]) != 15<<28|4096<<15 ||
		binary.BigEndian.Uint32(rows[16:20]) != 15<<28|4096<<15 ||
		binary.BigEndian.Uint32(rows[20:24]) != 14<<28|4096<<15 ||
		binary.BigEndian.Uint32(rows[32:36]) != 14<<28|4096<<15 ||
		binary.BigEndian.Uint32(rows[36:40]) != 14<<28|4096<<15 {
		t.Fatalf("default filter words are incorrectly encoded: %x", rows)
	}
}

func TestExtendedVLANCreateRejectsOLTWriteToReadOnlyDefault(t *testing.T) {
	store, err := New(nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	err = store.Create(me.ExtendedVlanTaggingOperationConfigurationDataClassID,
		extendedVLANEntityID, me.AttributeValueMap{
			me.ExtendedVlanTaggingOperationConfigurationData_AssociationType:                               uint8(2),
			me.ExtendedVlanTaggingOperationConfigurationData_AssociatedMePointer:                           uint16(0x101),
			me.ExtendedVlanTaggingOperationConfigurationData_ReceivedFrameVlanTaggingOperationTableMaxSize: uint16(1),
		})
	var result *ResultError
	if !errors.As(err, &result) || result.Result != me.AttributeFailure || result.FailedMask != 0x4000 {
		t.Fatalf("Create(read-only table size) error = %#v, want attribute failure mask 0x4000", err)
	}
	if store.Exists(Key{ClassID: me.ExtendedVlanTaggingOperationConfigurationDataClassID,
		EntityID: extendedVLANEntityID}) {
		t.Fatal("rejected Create committed an instance")
	}
}

func TestSetTableExtendedVLANAddsReplacesDeletesAndSorts(t *testing.T) {
	store := newExtendedVLANStore(t, Options{})
	key := Key{ClassID: me.ExtendedVlanTaggingOperationConfigurationDataClassID, EntityID: extendedVLANEntityID}
	row20 := classicVLANRow(0x20, 1)
	row10 := classicVLANRow(0x10, 2)
	setTable(t, store, key, 0x0400, me.ExtendedVlanTaggingOperationConfigurationData_ReceivedFrameVlanTaggingOperationTable,
		append(append([]byte(nil), row20...), row10...))

	rows := getTable(t, store, key, 0x0400,
		me.ExtendedVlanTaggingOperationConfigurationData_ReceivedFrameVlanTaggingOperationTable)
	if rows.NumRows != 5 || !bytes.Equal(rows.Rows[:extendedVLANRowSize], row10) ||
		!bytes.Equal(rows.Rows[extendedVLANRowSize:2*extendedVLANRowSize], row20) {
		t.Fatalf("rows after insert = %x", rows.Rows)
	}

	replacement := classicVLANRow(0x10, 9)
	setTable(t, store, key, 0x0400, me.ExtendedVlanTaggingOperationConfigurationData_ReceivedFrameVlanTaggingOperationTable, replacement)
	rows = getTable(t, store, key, 0x0400,
		me.ExtendedVlanTaggingOperationConfigurationData_ReceivedFrameVlanTaggingOperationTable)
	if rows.NumRows != 5 || !bytes.Equal(rows.Rows[:extendedVLANRowSize], replacement) {
		t.Fatalf("rows after replacement = %x", rows.Rows)
	}

	deletion := append([]byte(nil), replacement...)
	for index := 8; index < len(deletion); index++ {
		deletion[index] = 0xff
	}
	setTable(t, store, key, 0x0400, me.ExtendedVlanTaggingOperationConfigurationData_ReceivedFrameVlanTaggingOperationTable, deletion)
	rows = getTable(t, store, key, 0x0400,
		me.ExtendedVlanTaggingOperationConfigurationData_ReceivedFrameVlanTaggingOperationTable)
	if rows.NumRows != 4 || bytes.Equal(rows.Rows[:extendedVLANRowSize], replacement) {
		t.Fatalf("rows after deletion = %x", rows.Rows)
	}
}

func TestSetTableExtendedVLANCapacityFailureIsAtomic(t *testing.T) {
	applyCalls := 0
	store := newExtendedVLANStore(t, Options{
		ExtendedVLANTableSize: 4,
		Applier: ApplyFunc(func(change Change) error {
			applyCalls++
			return nil
		}),
	})
	key := Key{ClassID: me.ExtendedVlanTaggingOperationConfigurationDataClassID, EntityID: extendedVLANEntityID}
	updates := append(classicVLANRow(1, 1), classicVLANRow(2, 2)...)
	err := store.SetTable(key, 0x0400, me.AttributeValueMap{
		me.ExtendedVlanTaggingOperationConfigurationData_ReceivedFrameVlanTaggingOperationTable: me.TableRows{
			NumRows: 2,
			Rows:    updates,
		},
	})
	var result *ResultError
	if !errors.As(err, &result) || result.Result != me.ProcessingError {
		t.Fatalf("SetTable() error = %#v, want ProcessingError", err)
	}
	if store.DataSync() != 1 || applyCalls != 1 {
		t.Fatalf("failed SetTable changed state: sync=%d apply calls=%d", store.DataSync(), applyCalls)
	}
	rows := getTable(t, store, key, 0x0400,
		me.ExtendedVlanTaggingOperationConfigurationData_ReceivedFrameVlanTaggingOperationTable)
	if rows.NumRows != 3 || !bytes.Equal(rows.Rows, extendedVLANDefaultRows) {
		t.Fatalf("failed SetTable committed rows = %x", rows.Rows)
	}
}

func TestSetTablePlatformFailureRollsBackTableAndDataSync(t *testing.T) {
	wantError := errors.New("hardware rejected table")
	store := newExtendedVLANStore(t, Options{Applier: ApplyFunc(func(change Change) error {
		if change.Operation == OperationSetTable {
			if change.After == nil {
				t.Fatal("SetTable change has no candidate instance")
			}
			return wantError
		}
		return nil
	})})
	key := Key{ClassID: me.ExtendedVlanTaggingOperationConfigurationDataClassID, EntityID: extendedVLANEntityID}
	err := store.SetTable(key, 0x0400, me.AttributeValueMap{
		me.ExtendedVlanTaggingOperationConfigurationData_ReceivedFrameVlanTaggingOperationTable: me.TableRows{
			NumRows: 1,
			Rows:    classicVLANRow(1, 1),
		},
	})
	var result *ResultError
	if !errors.As(err, &result) || result.Result != me.ProcessingError || !errors.Is(err, wantError) {
		t.Fatalf("SetTable() error = %#v, want wrapped platform ProcessingError", err)
	}
	if store.DataSync() != 1 {
		t.Fatalf("DataSync() = %d after rejected SetTable, want 1", store.DataSync())
	}
	rows := getTable(t, store, key, 0x0400,
		me.ExtendedVlanTaggingOperationConfigurationData_ReceivedFrameVlanTaggingOperationTable)
	if rows.NumRows != 3 || !bytes.Equal(rows.Rows, extendedVLANDefaultRows) {
		t.Fatalf("rejected SetTable committed rows = %x", rows.Rows)
	}
}

func TestSetTableEnhancedVLANControlAndOrdering(t *testing.T) {
	store := newExtendedVLANStore(t, Options{})
	key := Key{ClassID: me.ExtendedVlanTaggingOperationConfigurationDataClassID, EntityID: extendedVLANEntityID}
	row20 := enhancedVLANRow(1, 2, 0x20, 2)
	row10 := enhancedVLANRow(1, 0, 0x10, 1)
	setTable(t, store, key, 0x0040,
		me.ExtendedVlanTaggingOperationConfigurationData_EnhancedReceivedFrameClassificationAndProcessingTable,
		append(append([]byte(nil), row20...), row10...))
	rows := getTable(t, store, key, 0x0040,
		me.ExtendedVlanTaggingOperationConfigurationData_EnhancedReceivedFrameClassificationAndProcessingTable)
	if rows.NumRows != 2 || binary.BigEndian.Uint16(rows.Rows[2:4]) != 0x10 ||
		binary.BigEndian.Uint16(rows.Rows[enhancedExtendedVLANRowSize+2:enhancedExtendedVLANRowSize+4]) != 0x20 ||
		rows.Rows[0]>>6 != 0 || rows.Rows[enhancedExtendedVLANRowSize]>>6 != 0 {
		t.Fatalf("stored enhanced rows = %x", rows.Rows)
	}

	setTable(t, store, key, 0x0040,
		me.ExtendedVlanTaggingOperationConfigurationData_EnhancedReceivedFrameClassificationAndProcessingTable,
		enhancedVLANRow(2, 0, 0x10, 0))
	rows = getTable(t, store, key, 0x0040,
		me.ExtendedVlanTaggingOperationConfigurationData_EnhancedReceivedFrameClassificationAndProcessingTable)
	if rows.NumRows != 1 || binary.BigEndian.Uint16(rows.Rows[2:4]) != 0x20 {
		t.Fatalf("enhanced rows after deletion = %x", rows.Rows)
	}

	setTable(t, store, key, 0x0040,
		me.ExtendedVlanTaggingOperationConfigurationData_EnhancedReceivedFrameClassificationAndProcessingTable,
		enhancedVLANRow(3, 0, 0, 0))
	rows = getTable(t, store, key, 0x0040,
		me.ExtendedVlanTaggingOperationConfigurationData_EnhancedReceivedFrameClassificationAndProcessingTable)
	if rows.NumRows != 0 || len(rows.Rows) != 0 {
		t.Fatalf("enhanced rows after clear = %x", rows.Rows)
	}
}

func TestSetTableEnhancedVLANRejectsReservedControlAndDirection(t *testing.T) {
	tests := []struct {
		name string
		row  []byte
	}{
		{name: "control", row: enhancedVLANRow(0, 0, 1, 0)},
		{name: "direction", row: enhancedVLANRow(1, 3, 1, 0)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newExtendedVLANStore(t, Options{})
			key := Key{ClassID: me.ExtendedVlanTaggingOperationConfigurationDataClassID, EntityID: extendedVLANEntityID}
			err := store.SetTable(key, 0x0040, me.AttributeValueMap{
				me.ExtendedVlanTaggingOperationConfigurationData_EnhancedReceivedFrameClassificationAndProcessingTable: me.TableRows{
					NumRows: 1,
					Rows:    test.row,
				},
			})
			var result *ResultError
			if !errors.As(err, &result) || result.Result != me.ParameterError {
				t.Fatalf("SetTable() error = %#v, want ParameterError", err)
			}
			if store.DataSync() != 1 {
				t.Fatalf("DataSync() = %d after rejected SetTable, want 1", store.DataSync())
			}
		})
	}
}

func newExtendedVLANStore(t *testing.T, options Options) *Store {
	t.Helper()
	factory := []Instance{{
		Key:        Key{ClassID: me.OnuDataClassID, EntityID: 0},
		Attributes: me.AttributeValueMap{me.OnuData_MibDataSync: uint8(0)},
	}}
	store, err := NewWithOptions(factory, options)
	if err != nil {
		t.Fatalf("NewWithOptions() error = %v", err)
	}
	if err := store.Create(me.ExtendedVlanTaggingOperationConfigurationDataClassID,
		extendedVLANEntityID, me.AttributeValueMap{
			me.ExtendedVlanTaggingOperationConfigurationData_AssociationType:     uint8(2),
			me.ExtendedVlanTaggingOperationConfigurationData_AssociatedMePointer: uint16(0x101),
			me.ExtendedVlanTaggingOperationConfigurationData_EnhancedMode:        uint8(1),
		}); err != nil {
		t.Fatalf("Create(extended VLAN) error = %v", err)
	}
	return store
}

func snapshotInstance(t *testing.T, store *Store, key Key) Instance {
	t.Helper()
	for _, instance := range store.Snapshot() {
		if instance.Key == key {
			return instance
		}
	}
	t.Fatalf("snapshot does not contain %#v", key)
	return Instance{}
}

func getTable(t *testing.T, store *Store, key Key, mask uint16, name string) me.TableRows {
	t.Helper()
	instance, err := store.Get(key, mask)
	if err != nil {
		t.Fatalf("Get(%s) error = %v", name, err)
	}
	rows, ok := instance.Attributes[name].(me.TableRows)
	if !ok {
		t.Fatalf("%s = %#v, want TableRows", name, instance.Attributes[name])
	}
	return rows
}

func setTable(t *testing.T, store *Store, key Key, mask uint16, name string, rows []byte) {
	t.Helper()
	definition, omciErr := me.GetAttributesDefinitions(key.ClassID)
	if omciErr.StatusCode() != me.Success {
		t.Fatalf("GetAttributesDefinitions() error = %v", omciErr.GetError())
	}
	attribute, err := me.GetAttributeDefinitionByName(definition, name)
	if err != nil {
		t.Fatalf("GetAttributeDefinitionByName() error = %v", err)
	}
	if err := store.SetTable(key, mask, me.AttributeValueMap{name: me.TableRows{
		NumRows: len(rows) / attribute.GetSize(),
		Rows:    rows,
	}}); err != nil {
		t.Fatalf("SetTable(%s) error = %v", name, err)
	}
}

func classicVLANRow(key, treatment byte) []byte {
	row := make([]byte, extendedVLANRowSize)
	row[7] = key
	row[15] = treatment
	return row
}

func enhancedVLANRow(control, direction byte, key uint16, treatment byte) []byte {
	row := make([]byte, enhancedExtendedVLANRowSize)
	row[0] = control<<6 | direction<<4
	binary.BigEndian.PutUint16(row[2:4], key)
	row[len(row)-1] = treatment
	return row
}

func tableRowsEqual(value interface{}, want me.TableRows) bool {
	got, ok := value.(me.TableRows)
	return ok && got.NumRows == want.NumRows && bytes.Equal(got.Rows, want.Rows)
}
