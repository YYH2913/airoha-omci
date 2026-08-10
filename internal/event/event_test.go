// SPDX-License-Identifier: Apache-2.0

package event

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	omci "github.com/opencord/omci-lib-go/v2"
	me "github.com/opencord/omci-lib-go/v2/generated"
	"github.com/xg2010g/airoha-omci/internal/engine"
	"github.com/xg2010g/airoha-omci/internal/mib"
)

func TestDecodeAndDispatchAlarmEvent(t *testing.T) {
	protocol := testEngine(t)
	event, err := Decode([]byte(`{"type":"alarm","class_id":11,"entity_id":257,"alarm_bit":0,"active":true}`))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	frames, err := event.Dispatch(protocol)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if len(frames) != 1 || omci.MessageType(frames[0][2]) != omci.AlarmNotificationType {
		t.Fatalf("alarm frames = %x", frames)
	}
	frames, err = event.Dispatch(protocol)
	if err != nil || len(frames) != 0 {
		t.Fatalf("duplicate Dispatch() frames=%d error=%v", len(frames), err)
	}
}

func TestDispatchAVCNormalizesNumericWidth(t *testing.T) {
	protocol := testEngine(t)
	event, err := Decode([]byte(`{"type":"avc","class_id":11,"entity_id":257,"format":"extended","attributes":{"OperationalState":0}}`))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	frames, err := event.Dispatch(protocol)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if len(frames) != 1 || omci.DeviceIdent(frames[0][3]) != omci.ExtendedIdent {
		t.Fatalf("AVC frames = %x", frames)
	}
}

func TestDispatchOpticalSample(t *testing.T) {
	protocol := opticalTestEngine(t)
	event, err := Decode([]byte(`{"type":"optical-sample","class_id":263,"entity_id":32769,"temperature":6400,"supply_voltage":33000,"laser_bias_current":2500,"transmit_power":10000,"receive_power":10}`))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	frames, err := event.Dispatch(protocol)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if len(frames) != 0 {
		t.Fatalf("normal optical sample frames = %x, want none", frames)
	}

	if _, err := Decode([]byte(`{"type":"optical-sample","class_id":263,"entity_id":32769,"receive_power":10,"unknown":1}`)); err == nil {
		t.Fatal("Decode(optical sample unknown field) error = nil")
	}
}

func TestDecodeRejectsUnknownAndTrailingFields(t *testing.T) {
	for _, input := range []string{
		`{"type":"alarm","extra":1}`,
		`{"type":"alarm"} {"type":"avc"}`,
	} {
		if _, err := Decode([]byte(input)); err == nil {
			t.Fatalf("Decode(%q) error = nil", input)
		}
	}
}

func TestExecSourceStreamsJSONLines(t *testing.T) {
	helper := filepath.Join(t.TempDir(), "events")
	script := "#!/bin/sh\nprintf '%s\\n' '# comment' '{\"type\":\"alarm\",\"class_id\":11,\"entity_id\":257,\"alarm_bit\":0,\"active\":true}'\n"
	if err := os.WriteFile(helper, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(helper) error = %v", err)
	}
	var received []Event
	err := (ExecSource{Path: helper}).Run(context.Background(), func(value Event) error {
		received = append(received, value)
		return nil
	})
	if err == nil || len(received) != 1 || received[0].Type != "alarm" {
		t.Fatalf("Run() events=%#v error=%v", received, err)
	}
}

func testEngine(t *testing.T) *engine.Engine {
	t.Helper()
	store, err := mib.New([]mib.Instance{
		{
			Key:        mib.Key{ClassID: me.OnuDataClassID, EntityID: 0},
			Attributes: me.AttributeValueMap{me.OnuData_MibDataSync: uint8(0)},
		},
		{
			Key: mib.Key{ClassID: me.PhysicalPathTerminationPointEthernetUniClassID, EntityID: 257},
			Attributes: me.AttributeValueMap{
				me.PhysicalPathTerminationPointEthernetUni_OperationalState: uint8(1),
			},
		},
	})
	if err != nil {
		t.Fatalf("mib.New() error = %v", err)
	}
	return engine.New(store)
}

func opticalTestEngine(t *testing.T) *engine.Engine {
	t.Helper()
	store, err := mib.New([]mib.Instance{{
		Key: mib.Key{ClassID: me.AniGClassID, EntityID: 0x8001},
		Attributes: me.AttributeValueMap{
			me.AniG_Arc:                         uint8(0),
			me.AniG_ArcInterval:                 uint8(0),
			me.AniG_OpticalSignalLevel:          uint16(0),
			me.AniG_LowerOpticalThreshold:       uint8(0xff),
			me.AniG_UpperOpticalThreshold:       uint8(0xff),
			me.AniG_TransmitOpticalLevel:        uint16(0),
			me.AniG_LowerTransmitPowerThreshold: uint8(0x81),
			me.AniG_UpperTransmitPowerThreshold: uint8(0x81),
		},
	}})
	if err != nil {
		t.Fatalf("mib.New() error = %v", err)
	}
	return engine.New(store)
}
