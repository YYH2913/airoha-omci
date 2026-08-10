// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"testing"
	"time"

	omci "github.com/opencord/omci-lib-go/v2"
	me "github.com/opencord/omci-lib-go/v2/generated"
	"github.com/xg2010g/airoha-omci/internal/mib"
	"github.com/xg2010g/airoha-omci/internal/optical"
)

const opticalANI = uint16(0x8001)

func TestOpticalSampleUpdatesANIGAndAppliesHysteresis(t *testing.T) {
	protocol, store := newOpticalEngine(t, 0, 0, 40, 0)
	key := mib.Key{ClassID: me.AniGClassID, EntityID: opticalANI}

	frames, err := protocol.NotifyOpticalSample(key, optical.Sample{
		LaserBiasCurrent: 2500, TransmitPower: 10000, ReceivePower: 10,
	}, omci.BaselineIdent)
	if err != nil {
		t.Fatalf("NotifyOpticalSample(low) error = %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("low optical frames = %d, want 1", len(frames))
	}
	alarm := decodeResponse(t, frames[0]).Layer(omci.LayerTypeAlarmNotification).(*omci.AlarmNotificationMsg)
	if alarm.AlarmBitmap[0] != 0x80 || alarm.AlarmSequenceNumber != 1 {
		t.Fatalf("low optical alarm = %#v", alarm)
	}
	instance, err := store.Get(key, 0x0044)
	if err != nil {
		t.Fatalf("Get(ANI levels) error = %v", err)
	}
	if got := int16(instance.Attributes[me.AniG_OpticalSignalLevel].(uint16)); got != -15000 {
		t.Fatalf("optical signal level = %d, want -15000", got)
	}
	if got := int16(instance.Attributes[me.AniG_TransmitOpticalLevel].(uint16)); got != 0 {
		t.Fatalf("transmit optical level = %d, want 0", got)
	}

	// -20 dBm is above the raise threshold but below its -19.5 dBm clear
	// boundary, so the alarm remains active without another notification.
	frames, err = protocol.NotifyOpticalSample(key, optical.Sample{
		LaserBiasCurrent: 2500, TransmitPower: 10000, ReceivePower: 100,
	}, omci.BaselineIdent)
	if err != nil || len(frames) != 0 {
		t.Fatalf("hysteresis sample frames=%d error=%v", len(frames), err)
	}

	negotiateExtended(t, protocol)
	frames, err = protocol.NotifyOpticalSample(key, optical.Sample{
		LaserBiasCurrent: 2500, TransmitPower: 10000, ReceivePower: 120,
	}, omci.ExtendedIdent)
	if err != nil || len(frames) != 1 {
		t.Fatalf("clear optical frames=%d error=%v", len(frames), err)
	}
	alarm = decodeResponse(t, frames[0]).Layer(omci.LayerTypeAlarmNotification).(*omci.AlarmNotificationMsg)
	if alarm.AlarmBitmap != ([28]byte{}) || alarm.AlarmSequenceNumber != 2 || !alarm.Extended {
		t.Fatalf("clear optical alarm = %#v", alarm)
	}
}

func TestARCRecordsAndFiltersAlarmThenExpiresAfterProblemFreeInterval(t *testing.T) {
	protocol, store := newOpticalEngine(t, 1, 1, 40, 0)
	key := mib.Key{ClassID: me.AniGClassID, EntityID: opticalANI}
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	protocol.now = func() time.Time { return now }

	frames, err := protocol.NotifyOpticalSample(key, optical.Sample{
		LaserBiasCurrent: 2500, TransmitPower: 10000, ReceivePower: 10,
	}, omci.BaselineIdent)
	if err != nil || len(frames) != 0 {
		t.Fatalf("ARC low sample frames=%d error=%v", len(frames), err)
	}

	request := encodeRequest(t, 80, omci.GetAllAlarmsRequestType, &omci.GetAllAlarmsRequest{
		MeBasePacket: omci.MeBasePacket{EntityClass: me.OnuDataClassID}, AlarmRetrievalMode: 0,
	})
	response, err := protocol.Handle(request)
	if err != nil {
		t.Fatalf("Handle(GetAllAlarms regardless ARC) error = %v", err)
	}
	audit := decodeResponse(t, response).Layer(omci.LayerTypeGetAllAlarmsResponse).(*omci.GetAllAlarmsResponse)
	if audit.NumberOfCommands != 1 {
		t.Fatalf("all alarm audit count = %d, want 1", audit.NumberOfCommands)
	}

	request = encodeRequest(t, 81, omci.GetAllAlarmsRequestType, &omci.GetAllAlarmsRequest{
		MeBasePacket: omci.MeBasePacket{EntityClass: me.OnuDataClassID}, AlarmRetrievalMode: 1,
	})
	response, err = protocol.Handle(request)
	if err != nil {
		t.Fatalf("Handle(GetAllAlarms excluding ARC) error = %v", err)
	}
	audit = decodeResponse(t, response).Layer(omci.LayerTypeGetAllAlarmsResponse).(*omci.GetAllAlarmsResponse)
	if audit.NumberOfCommands != 0 {
		t.Fatalf("non-ARC alarm audit count = %d, want 0", audit.NumberOfCommands)
	}

	frames, err = protocol.NotifyOpticalSample(key, optical.Sample{
		LaserBiasCurrent: 2500, TransmitPower: 10000, ReceivePower: 120,
	}, omci.BaselineIdent)
	if err != nil || len(frames) != 0 {
		t.Fatalf("ARC clear sample frames=%d error=%v", len(frames), err)
	}
	now = now.Add(59 * time.Second)
	if frames, err = protocol.PollARC(omci.BaselineIdent); err != nil || len(frames) != 0 {
		t.Fatalf("PollARC(59s) frames=%d error=%v", len(frames), err)
	}
	now = now.Add(time.Second)
	frames, err = protocol.PollARC(omci.BaselineIdent)
	if err != nil || len(frames) != 1 {
		t.Fatalf("PollARC(60s) frames=%d error=%v", len(frames), err)
	}
	avc := decodeResponse(t, frames[0]).Layer(omci.LayerTypeAttributeValueChange).(*omci.AttributeValueChangeMsg)
	if avc.AttributeMask != 0x0100 || avc.Attributes[me.AniG_Arc] != uint8(0) {
		t.Fatalf("ARC expiration AVC = %#v", avc)
	}
	instance, err := store.Get(key, 0x0100)
	if err != nil || instance.Attributes[me.AniG_Arc] != uint8(0) {
		t.Fatalf("ARC after expiration = %#v error=%v", instance.Attributes, err)
	}

	frames, err = protocol.NotifyOpticalSample(key, optical.Sample{
		LaserBiasCurrent: 2500, TransmitPower: 10000, ReceivePower: 10,
	}, omci.BaselineIdent)
	if err != nil || len(frames) != 1 {
		t.Fatalf("post-ARC alarm frames=%d error=%v", len(frames), err)
	}
	alarm := decodeResponse(t, frames[0]).Layer(omci.LayerTypeAlarmNotification).(*omci.AlarmNotificationMsg)
	if alarm.AlarmSequenceNumber != 1 {
		t.Fatalf("post-ARC alarm sequence = %d, want 1", alarm.AlarmSequenceNumber)
	}
}

func TestARCProblemFreeTimerRestartsWhenFaultRecurs(t *testing.T) {
	protocol, _ := newOpticalEngine(t, 1, 1, 40, 0)
	key := mib.Key{ClassID: me.AniGClassID, EntityID: opticalANI}
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	protocol.now = func() time.Time { return now }
	low := optical.Sample{LaserBiasCurrent: 2500, TransmitPower: 10000, ReceivePower: 10}
	good := optical.Sample{LaserBiasCurrent: 2500, TransmitPower: 10000, ReceivePower: 120}

	_, _ = protocol.NotifyOpticalSample(key, low, omci.BaselineIdent)
	_, _ = protocol.NotifyOpticalSample(key, good, omci.BaselineIdent)
	now = now.Add(30 * time.Second)
	_, _ = protocol.NotifyOpticalSample(key, low, omci.BaselineIdent)
	now = now.Add(30 * time.Second)
	_, _ = protocol.NotifyOpticalSample(key, good, omci.BaselineIdent)
	now = now.Add(59 * time.Second)
	if frames, err := protocol.PollARC(omci.BaselineIdent); err != nil || len(frames) != 0 {
		t.Fatalf("PollARC(after recurrence 59s) frames=%d error=%v", len(frames), err)
	}
	now = now.Add(time.Second)
	if frames, err := protocol.PollARC(omci.BaselineIdent); err != nil || len(frames) != 1 {
		t.Fatalf("PollARC(after recurrence 60s) frames=%d error=%v", len(frames), err)
	}
}

func TestThresholdSetReevaluatesLastOpticalSample(t *testing.T) {
	protocol, _ := newOpticalEngine(t, 0, 0, 0xff, 0xff)
	key := mib.Key{ClassID: me.AniGClassID, EntityID: opticalANI}
	if frames, err := protocol.NotifyOpticalSample(key, optical.Sample{
		LaserBiasCurrent: 2500, TransmitPower: 10000, ReceivePower: 10,
	}, omci.BaselineIdent); err != nil || len(frames) != 0 {
		t.Fatalf("initial sample frames=%d error=%v", len(frames), err)
	}

	request := encodeRequest(t, 90, omci.SetRequestType, &omci.SetRequest{
		MeBasePacket:  omci.MeBasePacket{EntityClass: me.AniGClassID, EntityInstance: opticalANI},
		AttributeMask: 0x0020,
		Attributes:    me.AttributeValueMap{me.AniG_LowerOpticalThreshold: uint8(40)},
	})
	response, err := protocol.Handle(request)
	if err != nil {
		t.Fatalf("Handle(Set lower threshold) error = %v", err)
	}
	set := decodeResponse(t, response).Layer(omci.LayerTypeSetResponse).(*omci.SetResponse)
	if set.Result != me.Success {
		t.Fatalf("Set lower threshold result = %v", set.Result)
	}
	frames := protocol.DrainNotifications()
	if len(frames) != 1 {
		t.Fatalf("threshold Set notifications = %d, want 1", len(frames))
	}
	alarm := decodeResponse(t, frames[0]).Layer(omci.LayerTypeAlarmNotification).(*omci.AlarmNotificationMsg)
	if alarm.AlarmBitmap[0] != 0x80 {
		t.Fatalf("threshold Set alarm bitmap = %x", alarm.AlarmBitmap)
	}
}

func TestTransmitAndLaserBiasAlarmsUseConfiguredAndInternalThresholds(t *testing.T) {
	protocol, store := newOpticalEngine(t, 0, 0, 0xff, 0xff)
	key := mib.Key{ClassID: me.AniGClassID, EntityID: opticalANI}
	if err := store.Set(key, me.AttributeValueMap{
		me.AniG_LowerTransmitPowerThreshold: uint8(0xfe), // -1 dBm
		me.AniG_UpperTransmitPowerThreshold: uint8(2),    // +1 dBm
	}); err != nil {
		t.Fatalf("Set(transmit thresholds) error = %v", err)
	}

	frames, err := protocol.NotifyOpticalSample(key, optical.Sample{
		LaserBiasCurrent: 2500, TransmitPower: 100, ReceivePower: 10,
	}, omci.BaselineIdent)
	if err != nil || len(frames) != 1 {
		t.Fatalf("low transmit frames=%d error=%v", len(frames), err)
	}
	alarm := decodeResponse(t, frames[0]).Layer(omci.LayerTypeAlarmNotification).(*omci.AlarmNotificationMsg)
	if alarm.AlarmBitmap[0] != 0x08 {
		t.Fatalf("low transmit bitmap = %x, want bit 4", alarm.AlarmBitmap)
	}

	frames, err = protocol.NotifyOpticalSample(key, optical.Sample{
		LaserBiasCurrent: 2500, TransmitPower: 10000, ReceivePower: 10,
	}, omci.BaselineIdent)
	if err != nil || len(frames) != 1 {
		t.Fatalf("normal transmit frames=%d error=%v", len(frames), err)
	}
	frames, err = protocol.NotifyOpticalSample(key, optical.Sample{
		LaserBiasCurrent: 2500, TransmitPower: 20000, ReceivePower: 10,
	}, omci.BaselineIdent)
	if err != nil || len(frames) != 1 {
		t.Fatalf("high transmit frames=%d error=%v", len(frames), err)
	}
	alarm = decodeResponse(t, frames[0]).Layer(omci.LayerTypeAlarmNotification).(*omci.AlarmNotificationMsg)
	if alarm.AlarmBitmap[0] != 0x04 {
		t.Fatalf("high transmit bitmap = %x, want bit 5", alarm.AlarmBitmap)
	}

	frames, err = protocol.NotifyOpticalSample(key, optical.Sample{
		LaserBiasCurrent: 0xa606, TransmitPower: 10000, ReceivePower: 10,
	}, omci.BaselineIdent)
	if err != nil || len(frames) != 1 {
		t.Fatalf("laser bias frames=%d error=%v", len(frames), err)
	}
	alarm = decodeResponse(t, frames[0]).Layer(omci.LayerTypeAlarmNotification).(*omci.AlarmNotificationMsg)
	if alarm.AlarmBitmap[0] != 0x02 {
		t.Fatalf("laser bias bitmap = %x, want bit 6", alarm.AlarmBitmap)
	}
	if frames, err = protocol.NotifyOpticalSample(key, optical.Sample{
		LaserBiasCurrent: 0x9d00, TransmitPower: 10000, ReceivePower: 10,
	}, omci.BaselineIdent); err != nil || len(frames) != 0 {
		t.Fatalf("bias hysteresis frames=%d error=%v", len(frames), err)
	}
	if frames, err = protocol.NotifyOpticalSample(key, optical.Sample{
		LaserBiasCurrent: 39000, TransmitPower: 10000, ReceivePower: 10,
	}, omci.BaselineIdent); err != nil || len(frames) != 1 {
		t.Fatalf("bias clear frames=%d error=%v", len(frames), err)
	}
}

func TestARCZeroExpiresImmediatelyAnd255NeverExpires(t *testing.T) {
	for _, test := range []struct {
		name     string
		interval uint8
		wantAVC  int
		wantARC  uint8
		advance  time.Duration
	}{
		{name: "immediate", interval: 0, wantAVC: 1, wantARC: 0},
		{name: "never", interval: 255, wantAVC: 0, wantARC: 1, advance: 10 * 365 * 24 * time.Hour},
	} {
		t.Run(test.name, func(t *testing.T) {
			protocol, store := newOpticalEngine(t, 0, 0, 0xff, 0xff)
			key := mib.Key{ClassID: me.AniGClassID, EntityID: opticalANI}
			now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
			protocol.now = func() time.Time { return now }
			if _, err := protocol.NotifyOpticalSample(key, optical.Sample{
				LaserBiasCurrent: 2500, TransmitPower: 10000, ReceivePower: 10,
			}, omci.BaselineIdent); err != nil {
				t.Fatalf("NotifyOpticalSample() error = %v", err)
			}

			request := encodeRequest(t, 100, omci.SetRequestType, &omci.SetRequest{
				MeBasePacket:  omci.MeBasePacket{EntityClass: me.AniGClassID, EntityInstance: opticalANI},
				AttributeMask: 0x0180,
				Attributes: me.AttributeValueMap{
					me.AniG_Arc: uint8(1), me.AniG_ArcInterval: test.interval,
				},
			})
			response, err := protocol.Handle(request)
			if err != nil {
				t.Fatalf("Handle(Set ARC) error = %v", err)
			}
			if result := decodeResponse(t, response).Layer(omci.LayerTypeSetResponse).(*omci.SetResponse).Result; result != me.Success {
				t.Fatalf("Set ARC result = %v", result)
			}
			frames := protocol.DrainNotifications()
			if len(frames) != test.wantAVC {
				t.Fatalf("Set ARC notifications = %d, want %d", len(frames), test.wantAVC)
			}
			now = now.Add(test.advance)
			if polled, err := protocol.PollARC(omci.BaselineIdent); err != nil || len(polled) != 0 {
				t.Fatalf("PollARC() frames=%d error=%v", len(polled), err)
			}
			instance, err := store.Get(key, 0x0100)
			if err != nil || instance.Attributes[me.AniG_Arc] != test.wantARC {
				t.Fatalf("ARC value = %#v error=%v, want %d", instance.Attributes, err, test.wantARC)
			}
		})
	}
}

func newOpticalEngine(t *testing.T, arc, interval, lowerReceive, upperReceive uint8) (*Engine, *mib.Store) {
	t.Helper()
	store, err := mib.New([]mib.Instance{{
		Key: mib.Key{ClassID: me.AniGClassID, EntityID: opticalANI},
		Attributes: me.AttributeValueMap{
			me.AniG_Arc:                         arc,
			me.AniG_ArcInterval:                 interval,
			me.AniG_OpticalSignalLevel:          uint16(0),
			me.AniG_LowerOpticalThreshold:       lowerReceive,
			me.AniG_UpperOpticalThreshold:       upperReceive,
			me.AniG_TransmitOpticalLevel:        uint16(0),
			me.AniG_LowerTransmitPowerThreshold: uint8(0x81),
			me.AniG_UpperTransmitPowerThreshold: uint8(0x81),
		},
	}})
	if err != nil {
		t.Fatalf("mib.New() error = %v", err)
	}
	return New(store), store
}
