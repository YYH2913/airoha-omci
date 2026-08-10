// SPDX-License-Identifier: Apache-2.0

package model

import (
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
	var ani mib.Instance
	for _, item := range store.Snapshot() {
		switch item.ClassID {
		case me.AniGClassID:
			ani = item
		case me.PhysicalPathTerminationPointEthernetUniClassID:
			ethernetUNIs++
			configurations[item.EntityID] = item.Attributes[me.PhysicalPathTerminationPointEthernetUni_ConfigurationInd].(uint8)
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
	for index, want := range ethernetConfiguration {
		entityID := uint16(ethernetCardID + index)
		if got := configurations[entityID]; got != want {
			t.Fatalf("Ethernet UNI %#x configuration = %d, want %d", entityID, got, want)
		}
	}
}

func TestXG2010GRejectsMalformedSerial(t *testing.T) {
	if _, err := XG2010G(Identity{SerialNumber: "bad"}); err == nil {
		t.Fatal("XG2010G() error = nil, want malformed serial error")
	}
}
