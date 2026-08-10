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
	for _, item := range store.Snapshot() {
		switch item.ClassID {
		case me.PhysicalPathTerminationPointEthernetUniClassID:
			ethernetUNIs++
		case me.TContClassID:
			tconts++
		}
	}
	if ethernetUNIs != 4 {
		t.Fatalf("Ethernet UNI count = %d, want 4", ethernetUNIs)
	}
	if tconts != defaultTContCount {
		t.Fatalf("T-CONT count = %d, want %d", tconts, defaultTContCount)
	}
}

func TestXG2010GRejectsMalformedSerial(t *testing.T) {
	if _, err := XG2010G(Identity{SerialNumber: "bad"}); err == nil {
		t.Fatal("XG2010G() error = nil, want malformed serial error")
	}
}
