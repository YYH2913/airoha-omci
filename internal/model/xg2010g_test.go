// SPDX-License-Identifier: Apache-2.0

package model

import (
	"slices"
	"testing"

	me "github.com/opencord/omci-lib-go/v2/generated"
	"github.com/xg2010g/airoha-omci/internal/mib"
)

func TestXG2010GFactoryMIB(t *testing.T) {
	factory, err := XG2010G(Identity{SerialNumber: "ABCD01020304"})
	if err != nil {
		t.Fatalf("XG2010G() error = %v", err)
	}
	store, err := mib.New(factory)
	if err != nil {
		t.Fatalf("mib.New() error = %v", err)
	}

	var ethernetUNIs int
	var tconts int
	var schedulers int
	var queues int
	configurations := make(map[uint16]uint8)
	sensedTypes := make(map[uint16]uint8)
	var ani mib.Instance
	var onu2 mib.Instance
	circuitPacks := make(map[uint16]mib.Instance)
	for _, item := range store.Snapshot() {
		switch item.ClassID {
		case me.AniGClassID:
			ani = item
		case me.Onu2GClassID:
			onu2 = item
		case me.CircuitPackClassID:
			circuitPacks[item.EntityID] = item
		case me.PhysicalPathTerminationPointEthernetUniClassID:
			ethernetUNIs++
			configurations[item.EntityID] = item.Attributes[me.PhysicalPathTerminationPointEthernetUni_ConfigurationInd].(uint8)
			sensedTypes[item.EntityID] = item.Attributes[me.PhysicalPathTerminationPointEthernetUni_SensedType].(uint8)
		case me.TContClassID:
			tconts++
		case me.TrafficSchedulerClassID:
			schedulers++
		case me.PriorityQueueClassID:
			queues++
		}
	}
	if ani.EntityID != aniEntityID || ani.Attributes[me.AniG_Arc] != uint8(0) ||
		ani.Attributes[me.AniG_ArcInterval] != uint8(0) ||
		ani.Attributes[me.AniG_LowerOpticalThreshold] != uint8(0xff) ||
		ani.Attributes[me.AniG_UpperOpticalThreshold] != uint8(0xff) ||
		ani.Attributes[me.AniG_LowerTransmitPowerThreshold] != uint8(0x81) ||
		ani.Attributes[me.AniG_UpperTransmitPowerThreshold] != uint8(0x81) {
		t.Fatalf("ANI-G defaults = %#v", ani)
	}
	if ethernetUNIs != 4 {
		t.Fatalf("Ethernet UNI count = %d, want 4", ethernetUNIs)
	}
	if tconts != defaultTContCount {
		t.Fatalf("T-CONT count = %d, want %d", tconts, defaultTContCount)
	}
	if schedulers != defaultTContCount {
		t.Fatalf("traffic scheduler count = %d, want %d", schedulers, defaultTContCount)
	}
	wantQueues := (defaultTContCount + len(ethernetConfiguration)) * queuesPerPort
	if queues != wantQueues {
		t.Fatalf("priority queue count = %d, want %d", queues, wantQueues)
	}
	if onu2.Attributes[me.Onu2G_TotalPriorityQueueNumber] != uint16(0) ||
		onu2.Attributes[me.Onu2G_TotalTrafficSchedulerNumber] != uint8(0) {
		t.Fatalf("ONU2-G global QoS resources = %#v, want no resources outside circuit packs", onu2.Attributes)
	}
	if got := circuitPacks[ethernetCardID].Attributes[me.CircuitPack_TotalPriorityQueueNumber]; got != uint8(len(ethernetConfiguration)*queuesPerPort) {
		t.Fatalf("Ethernet circuit-pack queues = %#v, want %d", got,
			len(ethernetConfiguration)*queuesPerPort)
	}
	if got := circuitPacks[ethernetCardID].Attributes[me.CircuitPack_Type]; got != uint8(45) {
		t.Fatalf("Ethernet circuit-pack type = %#v, want mixed services equipment", got)
	}
	if got := circuitPacks[aniCardID].Attributes[me.CircuitPack_TotalPriorityQueueNumber]; got != uint8(defaultTContCount*queuesPerPort) {
		t.Fatalf("ANI circuit-pack queues = %#v, want %d", got, defaultTContCount*queuesPerPort)
	}
	if got := circuitPacks[aniCardID].Attributes[me.CircuitPack_TotalTrafficSchedulerNumber]; got != uint8(defaultTContCount) {
		t.Fatalf("ANI circuit-pack schedulers = %#v, want %d", got, defaultTContCount)
	}
	for index, want := range ethernetConfiguration {
		entityID := uint16(ethernetCardID + index)
		if got := configurations[entityID]; got != want {
			t.Fatalf("Ethernet UNI %#x configuration = %d, want %d", entityID, got, want)
		}
		if got := sensedTypes[entityID]; got != ethernetSensedType[index] {
			t.Fatalf("Ethernet UNI %#x sensed type = %d, want %d",
				entityID, got, ethernetSensedType[index])
		}
	}
}

func TestXG2010GRejectsMalformedSerial(t *testing.T) {
	if _, err := XG2010G(Identity{SerialNumber: "bad"}); err == nil {
		t.Fatal("XG2010G() error = nil, want malformed serial error")
	}
}

func TestXG2010GSupportedClassesAreExplicitAndSorted(t *testing.T) {
	classes := XG2010GSupportedClasses()
	if !slices.IsSorted(classes) {
		t.Fatalf("supported classes are not sorted: %v", classes)
	}
	seen := make(map[me.ClassID]struct{}, len(classes))
	for _, classID := range classes {
		if _, duplicate := seen[classID]; duplicate {
			t.Fatalf("duplicate supported class %v", classID)
		}
		seen[classID] = struct{}{}
	}
	for _, required := range []me.ClassID{
		me.OmciClassID, me.ManagedEntityMeClassID, me.AttributeMeClassID,
		me.GemPortNetworkCtpClassID, me.ExtendedVlanTaggingOperationConfigurationDataClassID,
	} {
		if _, present := seen[required]; !present {
			t.Fatalf("supported classes omit %v", required)
		}
	}
	if _, present := seen[me.IpHostConfigDataClassID]; present {
		t.Fatalf("unsupported IP host class is advertised")
	}
}
