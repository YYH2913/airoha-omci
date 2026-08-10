// SPDX-License-Identifier: Apache-2.0

package mib

import (
	"errors"
	"testing"

	me "github.com/opencord/omci-lib-go/v2/generated"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := New([]Instance{{
		Key: Key{ClassID: me.OnuDataClassID, EntityID: 0},
		Attributes: me.AttributeValueMap{
			me.OnuData_MibDataSync: uint8(0),
		},
	}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return store
}

func TestCreateSetDeleteAdvancesDataSync(t *testing.T) {
	store := newTestStore(t)
	key := Key{ClassID: me.GalEthernetProfileClassID, EntityID: 1}

	err := store.Create(key.ClassID, key.EntityID, me.AttributeValueMap{
		me.GalEthernetProfile_MaximumGemPayloadSize: uint16(48),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if got := store.DataSync(); got != 1 {
		t.Fatalf("DataSync() = %d, want 1", got)
	}

	err = store.Set(key, me.AttributeValueMap{
		me.GalEthernetProfile_MaximumGemPayloadSize: uint16(64),
	})
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	instance, err := store.Get(key, 0x8000)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got := instance.Attributes[me.GalEthernetProfile_MaximumGemPayloadSize]; got != uint16(64) {
		t.Fatalf("MaximumGemPayloadSize = %#v, want 64", got)
	}

	if err := store.Delete(key); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if got := store.DataSync(); got != 3 {
		t.Fatalf("DataSync() = %d, want 3", got)
	}
	_, err = store.Get(key, 0x8000)
	var result *ResultError
	if !errors.As(err, &result) || result.Result != me.UnknownInstance {
		t.Fatalf("Get(deleted) error = %v, want UnknownInstance", err)
	}
}

func TestResetRetainsOnlyONUCreatedInstances(t *testing.T) {
	store := newTestStore(t)
	err := store.Create(me.GalEthernetProfileClassID, 1, me.AttributeValueMap{
		me.GalEthernetProfile_MaximumGemPayloadSize: uint16(48),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if err := store.Reset(); err != nil {
		t.Fatalf("Reset() error = %v", err)
	}
	if got := store.DataSync(); got != 0 {
		t.Fatalf("DataSync() after reset = %d, want 0", got)
	}
	if got := len(store.Snapshot()); got != 1 {
		t.Fatalf("len(Snapshot()) = %d, want 1", got)
	}
}

func TestPlatformApplyFailureDoesNotCommitCandidate(t *testing.T) {
	wantError := errors.New("platform rejected service graph")
	store, err := NewWithApplier([]Instance{{
		Key: Key{ClassID: me.OnuDataClassID, EntityID: 0},
		Attributes: me.AttributeValueMap{
			me.OnuData_MibDataSync: uint8(0),
		},
	}}, ApplyFunc(func(change Change) error {
		if change.Operation != OperationCreate || change.MIBDataSync != 1 {
			t.Fatalf("change = %#v, want create/data-sync 1", change)
		}
		if len(change.Snapshot) != 2 {
			t.Fatalf("candidate snapshot has %d MEs, want 2", len(change.Snapshot))
		}
		return wantError
	}))
	if err != nil {
		t.Fatalf("NewWithApplier() error = %v", err)
	}

	err = store.Create(me.GalEthernetProfileClassID, 1, me.AttributeValueMap{
		me.GalEthernetProfile_MaximumGemPayloadSize: uint16(48),
	})
	var result *ResultError
	if !errors.As(err, &result) || result.Result != me.ProcessingError ||
		!errors.Is(err, wantError) {
		t.Fatalf("Create() error = %#v, want wrapped platform ProcessingError", err)
	}
	if got := store.DataSync(); got != 0 {
		t.Fatalf("DataSync() = %d after rejected apply, want 0", got)
	}
	if got := len(store.Snapshot()); got != 1 {
		t.Fatalf("len(Snapshot()) = %d after rejected apply, want 1", got)
	}
}

func TestPlatformChangeDoesNotAliasCommittedStore(t *testing.T) {
	store, err := NewWithApplier([]Instance{{
		Key: Key{ClassID: me.OnuDataClassID, EntityID: 0},
		Attributes: me.AttributeValueMap{
			me.OnuData_MibDataSync: uint8(0),
		},
	}}, ApplyFunc(func(change Change) error {
		change.After.Attributes[me.GalEthernetProfile_MaximumGemPayloadSize] = uint16(999)
		change.Snapshot[0].Attributes[me.OnuData_MibDataSync] = uint8(99)
		return nil
	}))
	if err != nil {
		t.Fatalf("NewWithApplier() error = %v", err)
	}
	key := Key{ClassID: me.GalEthernetProfileClassID, EntityID: 1}
	if err := store.Create(key.ClassID, key.EntityID, me.AttributeValueMap{
		me.GalEthernetProfile_MaximumGemPayloadSize: uint16(48),
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	instance, err := store.Get(key, 0x8000)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got := instance.Attributes[me.GalEthernetProfile_MaximumGemPayloadSize]; got != uint16(48) {
		t.Fatalf("MaximumGemPayloadSize = %#v, want 48", got)
	}
}

func TestRejectsWriteToReadOnlyAttribute(t *testing.T) {
	store, err := New([]Instance{{
		Key: Key{ClassID: me.Onu2GClassID, EntityID: 0},
		Attributes: me.AttributeValueMap{
			me.Onu2G_TotalGemPortIdNumber: uint16(256),
		},
	}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = store.Set(Key{ClassID: me.Onu2GClassID, EntityID: 0}, me.AttributeValueMap{
		me.Onu2G_TotalGemPortIdNumber: uint16(1),
	})
	var result *ResultError
	if !errors.As(err, &result) || result.Result != me.AttributeFailure || result.FailedMask != 0x0080 {
		t.Fatalf("Set(read-only) error = %#v, want attribute failure mask 0x0080", err)
	}
}

func TestSnapshotDoesNotAliasStore(t *testing.T) {
	store := newTestStore(t)
	snapshot := store.Snapshot()
	snapshot[0].Attributes[me.OnuData_MibDataSync] = uint8(99)

	instance, err := store.Get(Key{ClassID: me.OnuDataClassID, EntityID: 0}, 0x8000)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got := instance.Attributes[me.OnuData_MibDataSync]; got != uint8(0) {
		t.Fatalf("MibDataSync = %#v, want 0", got)
	}
}

func TestGetReturnsKnownAttributesWithUnsupportedMask(t *testing.T) {
	store := newTestStore(t)
	instance, err := store.Get(Key{ClassID: me.OnuDataClassID, EntityID: 0}, 0xc000)
	var result *ResultError
	if !errors.As(err, &result) || result.Result != me.AttributeFailure || result.UnsupportedMask != 0x4000 {
		t.Fatalf("Get() error = %#v, want unsupported mask 0x4000", err)
	}
	if got := instance.Attributes[me.OnuData_MibDataSync]; got != uint8(0) {
		t.Fatalf("MibDataSync = %#v, want 0", got)
	}
}

func TestUnknownClassReturnsUnknownEntity(t *testing.T) {
	store := newTestStore(t)
	_, err := store.Get(Key{ClassID: me.ClassID(0xfffe), EntityID: 1}, 0x8000)
	var result *ResultError
	if !errors.As(err, &result) || result.Result != me.UnknownEntity {
		t.Fatalf("Get(unknown class) error = %#v, want UnknownEntity", err)
	}
}
