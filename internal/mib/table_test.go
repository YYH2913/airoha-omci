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

func TestSetTableWithoutTableChangeDoesNotAdvanceDataSyncOrApply(t *testing.T) {
	applyCalls := 0
	store := newExtendedVLANStore(t, Options{Applier: ApplyFunc(func(Change) error {
		applyCalls++
		return nil
	})})
	key := Key{ClassID: me.ExtendedVlanTaggingOperationConfigurationDataClassID, EntityID: extendedVLANEntityID}
	row := classicVLANRow(0x20, 1)
	setTable(t, store, key, 0x0400,
		me.ExtendedVlanTaggingOperationConfigurationData_ReceivedFrameVlanTaggingOperationTable, row)
	if store.DataSync() != 2 || applyCalls != 2 {
		t.Fatalf("first SetTable state: sync=%d apply calls=%d, want 2/2", store.DataSync(), applyCalls)
	}

	setTable(t, store, key, 0x0400,
		me.ExtendedVlanTaggingOperationConfigurationData_ReceivedFrameVlanTaggingOperationTable, row)
	if store.DataSync() != 2 || applyCalls != 2 {
		t.Fatalf("unchanged SetTable changed state: sync=%d apply calls=%d", store.DataSync(), applyCalls)
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

func TestMulticastGEMCreateInitializesAddressTables(t *testing.T) {
	store, err := New(nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	const entityID = 0x300
	if err := store.Create(me.MulticastGemInterworkingTerminationPointClassID, entityID,
		me.AttributeValueMap{
			me.MulticastGemInterworkingTerminationPoint_GemPortNetworkCtpConnectivityPointer: uint16(0x400),
			me.MulticastGemInterworkingTerminationPoint_InterworkingOption:                   uint8(1),
			me.MulticastGemInterworkingTerminationPoint_ServiceProfilePointer:                uint16(0x100),
			me.MulticastGemInterworkingTerminationPoint_NotUsed1:                             uint16(0),
			me.MulticastGemInterworkingTerminationPoint_GalProfilePointer:                    uint16(0),
			me.MulticastGemInterworkingTerminationPoint_NotUsed2:                             uint8(0),
		}); err != nil {
		t.Fatalf("Create(multicast GEM IW) error = %v", err)
	}
	instance := snapshotInstance(t, store, Key{
		ClassID: me.MulticastGemInterworkingTerminationPointClassID, EntityID: entityID})
	for _, name := range []string{
		me.MulticastGemInterworkingTerminationPoint_Ipv4MulticastAddressTable,
		me.MulticastGemInterworkingTerminationPoint_Ipv6MulticastAddressTable,
	} {
		if got := instance.Attributes[name]; !tableRowsEqual(got, me.TableRows{}) {
			t.Fatalf("%s = %#v, want empty TableRows", name, got)
		}
	}
}

func TestSetTableMulticastAddressesReplaceDeleteAndSort(t *testing.T) {
	store := newMulticastTableStore(t)
	key := Key{ClassID: me.MulticastGemInterworkingTerminationPointClassID, EntityID: 0x300}

	row20 := multicastIPv4Row(200, 0x20, 0xe1000000, 0xe10000ff)
	row10 := multicastIPv4Row(200, 0x10, 0xe2000000, 0xe20000ff)
	setTable(t, store, key, 0x0080,
		me.MulticastGemInterworkingTerminationPoint_Ipv4MulticastAddressTable,
		append(append([]byte(nil), row20...), row10...))
	rows := getTable(t, store, key, 0x0080,
		me.MulticastGemInterworkingTerminationPoint_Ipv4MulticastAddressTable)
	if rows.NumRows != 2 || !bytes.Equal(rows.Rows[:multicastIPv4RowSize], row10) {
		t.Fatalf("stored IPv4 multicast rows = %x", rows.Rows)
	}

	replacement := multicastIPv4Row(200, 0x10, 0xef000000, 0xefffffff)
	setTable(t, store, key, 0x0080,
		me.MulticastGemInterworkingTerminationPoint_Ipv4MulticastAddressTable, replacement)
	rows = getTable(t, store, key, 0x0080,
		me.MulticastGemInterworkingTerminationPoint_Ipv4MulticastAddressTable)
	if rows.NumRows != 2 || !bytes.Equal(rows.Rows[:multicastIPv4RowSize], replacement) {
		t.Fatalf("replaced IPv4 multicast rows = %x", rows.Rows)
	}

	deletion := append([]byte(nil), replacement...)
	clear(deletion[4:])
	setTable(t, store, key, 0x0080,
		me.MulticastGemInterworkingTerminationPoint_Ipv4MulticastAddressTable, deletion)
	rows = getTable(t, store, key, 0x0080,
		me.MulticastGemInterworkingTerminationPoint_Ipv4MulticastAddressTable)
	if rows.NumRows != 1 || !bytes.Equal(rows.Rows, row20) {
		t.Fatalf("IPv4 multicast rows after delete = %x", rows.Rows)
	}

	ipv6 := multicastIPv6Row(201, 1, 0x100, 0x1ff)
	setTable(t, store, key, 0x0040,
		me.MulticastGemInterworkingTerminationPoint_Ipv6MulticastAddressTable, ipv6)
	rows = getTable(t, store, key, 0x0040,
		me.MulticastGemInterworkingTerminationPoint_Ipv6MulticastAddressTable)
	if rows.NumRows != 1 || !bytes.Equal(rows.Rows, ipv6) {
		t.Fatalf("stored IPv6 multicast rows = %x", rows.Rows)
	}
}

func TestSetTableMulticastAddressesRejectInvalidRowsAtomically(t *testing.T) {
	tests := []struct {
		name string
		mask uint16
		attr string
		row  []byte
	}{
		{name: "GEM", mask: 0x0080,
			attr: me.MulticastGemInterworkingTerminationPoint_Ipv4MulticastAddressTable,
			row:  multicastIPv4Row(4096, 1, 0xe0000000, 0xefffffff)},
		{name: "IPv4 unicast", mask: 0x0080,
			attr: me.MulticastGemInterworkingTerminationPoint_Ipv4MulticastAddressTable,
			row:  multicastIPv4Row(200, 1, 0x0a000001, 0x0a000002)},
		{name: "IPv4 reversed", mask: 0x0080,
			attr: me.MulticastGemInterworkingTerminationPoint_Ipv4MulticastAddressTable,
			row:  multicastIPv4Row(200, 1, 0xe1000002, 0xe1000001)},
		{name: "IPv6 unicast", mask: 0x0040,
			attr: me.MulticastGemInterworkingTerminationPoint_Ipv6MulticastAddressTable,
			row: func() []byte {
				row := multicastIPv6Row(200, 1, 1, 2)
				row[12] = 0x20
				return row
			}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newMulticastTableStore(t)
			err := store.SetTable(Key{ClassID: me.MulticastGemInterworkingTerminationPointClassID,
				EntityID: 0x300}, test.mask, me.AttributeValueMap{test.attr: me.TableRows{
				NumRows: 1, Rows: test.row,
			}})
			var result *ResultError
			if !errors.As(err, &result) || result.Result != me.ParameterError {
				t.Fatalf("SetTable() error = %#v, want ParameterError", err)
			}
			if store.DataSync() != 1 {
				t.Fatalf("DataSync() = %d after rejected row, want 1", store.DataSync())
			}
		})
	}
}

func newMulticastTableStore(t *testing.T) *Store {
	t.Helper()
	store, err := New([]Instance{{
		Key:        Key{ClassID: me.OnuDataClassID, EntityID: 0},
		Attributes: me.AttributeValueMap{me.OnuData_MibDataSync: uint8(0)},
	}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := store.Create(me.MulticastGemInterworkingTerminationPointClassID, 0x300,
		me.AttributeValueMap{
			me.MulticastGemInterworkingTerminationPoint_GemPortNetworkCtpConnectivityPointer: uint16(0x400),
			me.MulticastGemInterworkingTerminationPoint_InterworkingOption:                   uint8(1),
			me.MulticastGemInterworkingTerminationPoint_ServiceProfilePointer:                uint16(0x100),
			me.MulticastGemInterworkingTerminationPoint_NotUsed1:                             uint16(0),
			me.MulticastGemInterworkingTerminationPoint_GalProfilePointer:                    uint16(0),
			me.MulticastGemInterworkingTerminationPoint_NotUsed2:                             uint8(0),
		}); err != nil {
		t.Fatalf("Create(multicast GEM IW) error = %v", err)
	}
	return store
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
	row[19] = treatment
	return row
}

func multicastIPv4Row(gem, secondary uint16, start, stop uint32) []byte {
	row := make([]byte, multicastIPv4RowSize)
	binary.BigEndian.PutUint16(row[0:2], gem)
	binary.BigEndian.PutUint16(row[2:4], secondary)
	binary.BigEndian.PutUint32(row[4:8], start)
	binary.BigEndian.PutUint32(row[8:12], stop)
	return row
}

func multicastIPv6Row(gem, secondary uint16, start, stop uint32) []byte {
	row := make([]byte, multicastIPv6RowSize)
	binary.BigEndian.PutUint16(row[0:2], gem)
	binary.BigEndian.PutUint16(row[2:4], secondary)
	binary.BigEndian.PutUint32(row[4:8], start)
	binary.BigEndian.PutUint32(row[8:12], stop)
	row[12] = 0xff
	return row
}

func tableRowsEqual(value interface{}, want me.TableRows) bool {
	got, ok := value.(me.TableRows)
	return ok && got.NumRows == want.NumRows && bytes.Equal(got.Rows, want.Rows)
}
