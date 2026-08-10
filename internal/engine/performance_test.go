// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"errors"
	"math"
	"testing"
	"time"

	omci "github.com/opencord/omci-lib-go/v2"
	me "github.com/opencord/omci-lib-go/v2/generated"
	"github.com/xg2010g/airoha-omci/internal/mib"
	"github.com/xg2010g/airoha-omci/internal/optical"
	"github.com/xg2010g/airoha-omci/internal/performance"
)

const (
	testGEMEntity = uint16(0x101)
	testGEMPort   = uint16(42)
)

type recordingPerformanceController struct {
	counters         performance.GEMPortCounters
	ethernetCounters performance.EthernetCounters
	err              error
	calls            int
	ethernetCalls    int
	synced           time.Time
}

func (c *recordingPerformanceController) SynchronizeTime(value time.Time) error {
	c.synced = value
	return nil
}

func (c *recordingPerformanceController) Reboot(uint8) error { return nil }

func (c *recordingPerformanceController) OpticalLineSupervision() (optical.Diagnostics, error) {
	return optical.Diagnostics{}, nil
}

func (c *recordingPerformanceController) GEMPortCounters(portID uint16) (performance.GEMPortCounters, error) {
	c.calls++
	if portID != testGEMPort {
		return performance.GEMPortCounters{}, errors.New("unexpected GEM port")
	}
	return c.counters, c.err
}

func (c *recordingPerformanceController) EthernetCounters(entityID uint16) (performance.EthernetCounters, error) {
	c.ethernetCalls++
	if entityID < 0x0101 || entityID > 0x0104 {
		return performance.EthernetCounters{}, errors.New("unexpected Ethernet UNI")
	}
	return c.ethernetCounters, c.err
}

func TestEthernetUNIPerformanceCurrentAndHistory(t *testing.T) {
	protocol, store, controller := newEthernetPerformanceEngine(t, false)
	now := time.Date(2026, 8, 11, 4, 0, 0, 0, time.UTC)
	protocol.now = func() time.Time { return now }
	controller.ethernetCounters.Received = performance.EthernetDirectionCounters{
		Frames: 100, Octets: 1000, DropEvents: 2, MulticastFrames: 3,
		SizeBuckets: [6]uint64{4, 5, 6, 7, 8, 9},
	}
	createEthernetPerformance(t, protocol,
		me.EthernetPerformanceMonitoringHistoryData3ClassID, testGEMEntity, 0x701)
	if controller.ethernetCalls != 1 {
		t.Fatalf("Ethernet counter calls after Create = %d, want 1", controller.ethernetCalls)
	}
	pollPerformance(t, protocol)

	controller.ethernetCounters.Received.Frames = 111
	controller.ethernetCounters.Received.Octets = 1222
	controller.ethernetCounters.Received.DropEvents = 5
	response := getCurrentPerformance(t, protocol,
		me.EthernetPerformanceMonitoringHistoryData3ClassID, testGEMEntity, 0x3800, 0x702)
	if response.Result != me.Success ||
		response.Attributes[me.EthernetPerformanceMonitoringHistoryData3_DropEvents] != uint32(3) ||
		response.Attributes[me.EthernetPerformanceMonitoringHistoryData3_Octets] != uint32(222) ||
		response.Attributes[me.EthernetPerformanceMonitoringHistoryData3_Packets] != uint32(11) {
		t.Fatalf("Ethernet current performance = %#v", response)
	}

	now = now.Add(performanceInterval)
	controller.ethernetCounters.Received.Frames = 150
	controller.ethernetCounters.Received.Octets = 1600
	pollPerformance(t, protocol)
	history, err := store.Get(mib.Key{
		ClassID:  me.EthernetPerformanceMonitoringHistoryData3ClassID,
		EntityID: testGEMEntity,
	}, 0x9800)
	if err != nil {
		t.Fatalf("Get(Ethernet history) error = %v", err)
	}
	if history.Attributes[me.EthernetPerformanceMonitoringHistoryData3_IntervalEndTime] != uint8(1) ||
		history.Attributes[me.EthernetPerformanceMonitoringHistoryData3_Octets] != uint32(600) ||
		history.Attributes[me.EthernetPerformanceMonitoringHistoryData3_Packets] != uint32(50) {
		t.Fatalf("Ethernet history = %#v", history.Attributes)
	}
	if store.DataSync() != 1 {
		t.Fatalf("Ethernet PM rollover MIB data sync = %d, want Create-only value 1", store.DataSync())
	}
}

func TestEthernetFramePerformanceUsesBridgePortDirection(t *testing.T) {
	protocol, _, controller := newEthernetPerformanceEngine(t, true)
	controller.ethernetCounters = performance.EthernetCounters{
		Received:    performance.EthernetDirectionCounters{Frames: 100, Octets: 1000},
		Transmitted: performance.EthernetDirectionCounters{Frames: 200, Octets: 2000},
	}
	createEthernetPerformance(t, protocol,
		me.EthernetFramePerformanceMonitoringHistoryDataDownstreamClassID, 0x0201, 0x710)
	createEthernetPerformance(t, protocol,
		me.EthernetFramePerformanceMonitoringHistoryDataUpstreamClassID, 0x0201, 0x711)
	if !protocol.bridgePortHasPerformanceLocked(0x0201) {
		t.Fatal("bridge port performance instances are missing after Create")
	}

	controller.ethernetCounters.Received.Frames += 11
	controller.ethernetCounters.Received.Octets += 22
	controller.ethernetCounters.Transmitted.Frames += 33
	controller.ethernetCounters.Transmitted.Octets += 44
	downstream := getCurrentPerformance(t, protocol,
		me.EthernetFramePerformanceMonitoringHistoryDataDownstreamClassID,
		0x0201, 0x1800, 0x712)
	if downstream.Result != me.Success ||
		downstream.Attributes[me.EthernetFramePerformanceMonitoringHistoryDataDownstream_Octets] != uint32(44) ||
		downstream.Attributes[me.EthernetFramePerformanceMonitoringHistoryDataDownstream_Packets] != uint32(33) {
		t.Fatalf("downstream bridge performance = %#v", downstream)
	}
	upstream := getCurrentPerformance(t, protocol,
		me.EthernetFramePerformanceMonitoringHistoryDataUpstreamClassID,
		0x0201, 0x1800, 0x713)
	if upstream.Result != me.Success ||
		upstream.Attributes[me.EthernetFramePerformanceMonitoringHistoryDataUpstream_Octets] != uint32(22) ||
		upstream.Attributes[me.EthernetFramePerformanceMonitoringHistoryDataUpstream_Packets] != uint32(11) {
		t.Fatalf("upstream bridge performance = %#v", upstream)
	}
	if !protocol.bridgePortHasPerformanceLocked(0x0201) {
		t.Fatal("bridge port performance instances disappeared before Set")
	}

	request := encodeRequest(t, 0x714, omci.SetRequestType, &omci.SetRequest{
		MeBasePacket: omci.MeBasePacket{
			EntityClass: me.MacBridgePortConfigurationDataClassID, EntityInstance: 0x0201,
		},
		AttributeMask: 0x1000,
		Attributes: me.AttributeValueMap{
			me.MacBridgePortConfigurationData_TpPointer: uint16(0x0102),
		},
	})
	encoded, err := protocol.Handle(request)
	if err != nil {
		t.Fatalf("Handle(Set monitored bridge association) error = %v", err)
	}
	setResponse := decodeResponse(t, encoded).Layer(omci.LayerTypeSetResponse).(*omci.SetResponse)
	if setResponse.Result != me.ParameterError {
		t.Fatalf("Set monitored bridge association result = %v, want ParameterError", setResponse.Result)
	}
}

func TestGEMPerformanceCurrentAndHistoryIntervals(t *testing.T) {
	protocol, store, controller := newPerformanceEngine(t)
	now := time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC)
	protocol.now = func() time.Time { return now }
	controller.counters = performance.GEMPortCounters{
		ReceivedGEMFrames: 100, ReceivedPayloadBytes: 1000,
		TransmittedGEMFrames: 200, TransmittedPayloadBytes: 2000,
	}
	pollPerformance(t, protocol)
	if controller.calls != 1 {
		t.Fatalf("initial counter calls = %d, want 1", controller.calls)
	}

	controller.counters = performance.GEMPortCounters{
		ReceivedGEMFrames: 111, ReceivedPayloadBytes: 1022,
		TransmittedGEMFrames: 233, TransmittedPayloadBytes: 2044,
	}
	current := getCurrentGEMPerformance(t, protocol, 0x3800)
	if current.Result != me.Success ||
		current.Attributes[me.GemPortNetworkCtpPerformanceMonitoringHistoryData_ReceivedGemFrames] != uint32(11) ||
		current.Attributes[me.GemPortNetworkCtpPerformanceMonitoringHistoryData_TransmittedGemFrames] != uint32(33) ||
		current.Attributes[me.GemPortNetworkCtpPerformanceMonitoringHistoryData_ReceivedPayloadBytes] != uint64(22) {
		t.Fatalf("current performance = %#v", current)
	}
	history, err := store.Get(gemPerformanceKey(), 0xb800)
	if err != nil {
		t.Fatalf("Get(history before boundary) error = %v", err)
	}
	if history.Attributes[me.GemPortNetworkCtpPerformanceMonitoringHistoryData_IntervalEndTime] != uint8(0) ||
		history.Attributes[me.GemPortNetworkCtpPerformanceMonitoringHistoryData_ReceivedGemFrames] != uint32(0) {
		t.Fatalf("history before boundary = %#v", history.Attributes)
	}

	now = now.Add(performanceInterval)
	controller.counters = performance.GEMPortCounters{
		ReceivedGEMFrames: 150, ReceivedPayloadBytes: 1600,
		TransmittedGEMFrames: 280, TransmittedPayloadBytes: 2900,
	}
	pollPerformance(t, protocol)
	history, err = store.Get(gemPerformanceKey(), 0xbc00)
	if err != nil {
		t.Fatalf("Get(history) error = %v", err)
	}
	if history.Attributes[me.GemPortNetworkCtpPerformanceMonitoringHistoryData_IntervalEndTime] != uint8(1) ||
		history.Attributes[me.GemPortNetworkCtpPerformanceMonitoringHistoryData_ReceivedGemFrames] != uint32(50) ||
		history.Attributes[me.GemPortNetworkCtpPerformanceMonitoringHistoryData_TransmittedGemFrames] != uint32(80) ||
		history.Attributes[me.GemPortNetworkCtpPerformanceMonitoringHistoryData_ReceivedPayloadBytes] != uint64(600) ||
		history.Attributes[me.GemPortNetworkCtpPerformanceMonitoringHistoryData_TransmittedPayloadBytes] != uint64(900) {
		t.Fatalf("completed history = %#v", history.Attributes)
	}
	if store.DataSync() != 0 {
		t.Fatalf("PM rollover MIB data sync = %d, want 0", store.DataSync())
	}

	controller.counters.ReceivedGEMFrames = 175
	current = getCurrentGEMPerformance(t, protocol, 0x1000)
	if current.Result != me.Success ||
		current.Attributes[me.GemPortNetworkCtpPerformanceMonitoringHistoryData_ReceivedGemFrames] != uint32(25) {
		t.Fatalf("next current performance = %#v", current)
	}
}

func TestGEMPerformanceHandlesHardwareResetAndSaturation(t *testing.T) {
	start := performance.GEMPortCounters{
		ReceivedGEMFrames: 100, TransmittedGEMFrames: 200,
		ReceivedPayloadBytes: 300, TransmittedPayloadBytes: 400,
	}
	current := performance.GEMPortCounters{
		ReceivedGEMFrames: 5, TransmittedGEMFrames: math.MaxUint32 + 1000,
		ReceivedPayloadBytes: 7, TransmittedPayloadBytes: 9,
	}
	delta := deltaGEMCounters(start, current)
	if delta.ReceivedGEMFrames != 5 || delta.ReceivedPayloadBytes != 7 ||
		delta.TransmittedPayloadBytes != 9 || saturatingUint32(delta.TransmittedGEMFrames) != math.MaxUint32 {
		t.Fatalf("reset/saturated delta = %#v", delta)
	}
}

func TestGEMPerformanceRejectsOversizedCurrentRequestAndBackendFailure(t *testing.T) {
	protocol, _, controller := newPerformanceEngine(t)
	controller.counters = performance.GEMPortCounters{}
	pollPerformance(t, protocol)
	response := getCurrentGEMPerformance(t, protocol, 0xfe00)
	if response.Result != me.ParameterError {
		t.Fatalf("oversized current result = %v, want ParameterError", response.Result)
	}
	controller.err = errors.New("counter timeout")
	response = getCurrentGEMPerformance(t, protocol, 0x1000)
	if response.Result != me.ProcessingError {
		t.Fatalf("failed current result = %v, want ProcessingError", response.Result)
	}
}

func TestGEMPerformanceCreateRequiresParentAndCounterBackend(t *testing.T) {
	store := newTestStoreForPerformanceCreate(t)
	protocol := New(store)
	request := createGEMPerformanceRequest(t, 0x610)
	encoded, err := protocol.Handle(request)
	if err != nil {
		t.Fatalf("Handle(Create without backend) error = %v", err)
	}
	response := decodeResponse(t, encoded).Layer(omci.LayerTypeCreateResponse).(*omci.CreateResponse)
	if response.Result != me.NotSupported || store.Exists(gemPerformanceKey()) {
		t.Fatalf("Create without backend result=%v exists=%v", response.Result, store.Exists(gemPerformanceKey()))
	}

	controller := &recordingPerformanceController{}
	controller.counters = performance.GEMPortCounters{ReceivedGEMFrames: 10}
	protocol.SetPerformanceController(controller)
	encoded, err = protocol.Handle(createGEMPerformanceRequest(t, 0x611))
	if err != nil {
		t.Fatalf("Handle(Create PM) error = %v", err)
	}
	response = decodeResponse(t, encoded).Layer(omci.LayerTypeCreateResponse).(*omci.CreateResponse)
	if response.Result != me.Success || !store.Exists(gemPerformanceKey()) || controller.calls != 1 {
		t.Fatalf("Create PM result=%v exists=%v calls=%d", response.Result,
			store.Exists(gemPerformanceKey()), controller.calls)
	}

	deleteParent := encodeRequest(t, 0x612, omci.DeleteRequestType, &omci.DeleteRequest{
		MeBasePacket: omci.MeBasePacket{
			EntityClass: me.GemPortNetworkCtpClassID, EntityInstance: testGEMEntity,
		},
	})
	encoded, err = protocol.Handle(deleteParent)
	if err != nil {
		t.Fatalf("Handle(Delete parent) error = %v", err)
	}
	deleteResponse := decodeResponse(t, encoded).Layer(omci.LayerTypeDeleteResponse).(*omci.DeleteResponse)
	if deleteResponse.Result != me.ParameterError {
		t.Fatalf("Delete parent with PM result = %v, want ParameterError", deleteResponse.Result)
	}
}

func TestPerformanceThresholdPointerValidationAndReferenceProtection(t *testing.T) {
	store := newTestStoreForPerformanceCreate(t)
	protocol := New(store)
	controller := &recordingPerformanceController{}
	protocol.SetPerformanceController(controller)

	missing := createGEMPerformanceRequestWithThreshold(t, 0x630, 0x900)
	encoded, err := protocol.Handle(missing)
	if err != nil {
		t.Fatalf("Handle(Create PM with missing threshold) error = %v", err)
	}
	response := decodeResponse(t, encoded).Layer(omci.LayerTypeCreateResponse).(*omci.CreateResponse)
	if response.Result != me.ParameterError || store.Exists(gemPerformanceKey()) {
		t.Fatalf("missing threshold result=%v exists=%v", response.Result, store.Exists(gemPerformanceKey()))
	}

	if err := store.Create(me.ThresholdData1ClassID, 0x900, me.AttributeValueMap{
		me.ThresholdData1_ThresholdValue1: uint32(5),
	}); err != nil {
		t.Fatalf("Create(ThresholdData1) error = %v", err)
	}
	controller.counters = performance.GEMPortCounters{}
	encoded, err = protocol.Handle(createGEMPerformanceRequestWithThreshold(t, 0x631, 0x900))
	if err != nil {
		t.Fatalf("Handle(Create PM with threshold) error = %v", err)
	}
	response = decodeResponse(t, encoded).Layer(omci.LayerTypeCreateResponse).(*omci.CreateResponse)
	if response.Result != me.Success || !store.Exists(gemPerformanceKey()) {
		t.Fatalf("valid threshold result=%v exists=%v", response.Result, store.Exists(gemPerformanceKey()))
	}

	setMissing := encodeRequest(t, 0x632, omci.SetRequestType, &omci.SetRequest{
		MeBasePacket: omci.MeBasePacket{
			EntityClass:    me.GemPortNetworkCtpPerformanceMonitoringHistoryDataClassID,
			EntityInstance: testGEMEntity,
		},
		AttributeMask: 0x4000,
		Attributes: me.AttributeValueMap{
			me.GemPortNetworkCtpPerformanceMonitoringHistoryData_ThresholdData12Id: uint16(0x901),
		},
	})
	encoded, err = protocol.Handle(setMissing)
	if err != nil {
		t.Fatalf("Handle(Set missing threshold) error = %v", err)
	}
	setResponse := decodeResponse(t, encoded).Layer(omci.LayerTypeSetResponse).(*omci.SetResponse)
	if setResponse.Result != me.ParameterError {
		t.Fatalf("Set missing threshold result = %v, want ParameterError", setResponse.Result)
	}

	deleteThreshold := encodeRequest(t, 0x633, omci.DeleteRequestType, &omci.DeleteRequest{
		MeBasePacket: omci.MeBasePacket{EntityClass: me.ThresholdData1ClassID, EntityInstance: 0x900},
	})
	encoded, err = protocol.Handle(deleteThreshold)
	if err != nil {
		t.Fatalf("Handle(Delete referenced threshold) error = %v", err)
	}
	deleteResponse := decodeResponse(t, encoded).Layer(omci.LayerTypeDeleteResponse).(*omci.DeleteResponse)
	if deleteResponse.Result != me.ParameterError || !store.Exists(mib.Key{
		ClassID: me.ThresholdData1ClassID, EntityID: 0x900,
	}) {
		t.Fatalf("Delete referenced threshold result=%v exists=%v", deleteResponse.Result,
			store.Exists(mib.Key{ClassID: me.ThresholdData1ClassID, EntityID: 0x900}))
	}
}

func TestEthernetPerformanceThresholdCrossingAlertLifecycle(t *testing.T) {
	protocol, store, controller := newEthernetPerformanceEngine(t, false)
	now := time.Date(2026, 8, 11, 5, 0, 0, 0, time.UTC)
	protocol.now = func() time.Time { return now }
	thresholdID := uint16(0x910)
	if err := store.Create(me.ThresholdData1ClassID, thresholdID, me.AttributeValueMap{
		me.ThresholdData1_ThresholdValue1: uint32(5),
		me.ThresholdData1_ThresholdValue2: uint32(3),
	}); err != nil {
		t.Fatalf("Create(TCA ThresholdData1) error = %v", err)
	}
	controller.ethernetCounters.Received.DropEvents = 100
	createEthernetPerformanceWithThreshold(t, protocol,
		me.EthernetPerformanceMonitoringHistoryData3ClassID, testGEMEntity, thresholdID, 0x640)
	pollPerformance(t, protocol)

	controller.ethernetCounters.Received.DropEvents = 104
	if frames := pollPerformance(t, protocol); len(frames) != 0 {
		t.Fatalf("below-threshold TCA frames = %d, want 0", len(frames))
	}
	controller.ethernetCounters.Received.DropEvents = 105
	frames := pollPerformance(t, protocol)
	if len(frames) != 1 {
		t.Fatalf("first TCA frames = %d, want 1", len(frames))
	}
	alarm := decodeResponse(t, frames[0]).Layer(omci.LayerTypeAlarmNotification).(*omci.AlarmNotificationMsg)
	if alarm.EntityClass != me.EthernetPerformanceMonitoringHistoryData3ClassID ||
		alarm.EntityInstance != testGEMEntity || alarm.AlarmBitmap[0] != 0x80 ||
		alarm.AlarmSequenceNumber != 1 {
		t.Fatalf("first TCA = %#v", alarm)
	}

	controller.ethernetCounters.Received.UndersizeFrames = 3
	frames = pollPerformance(t, protocol)
	if len(frames) != 1 {
		t.Fatalf("second TCA frames = %d, want 1", len(frames))
	}
	alarm = decodeResponse(t, frames[0]).Layer(omci.LayerTypeAlarmNotification).(*omci.AlarmNotificationMsg)
	if alarm.AlarmBitmap[0] != 0xc0 || alarm.AlarmSequenceNumber != 2 {
		t.Fatalf("combined TCA = %#v", alarm)
	}

	now = now.Add(performanceInterval)
	controller.ethernetCounters.Received.DropEvents = 106
	frames = pollPerformance(t, protocol)
	if len(frames) != 1 || len(protocol.performanceTCA) != 0 || len(protocol.alarms) != 0 {
		t.Fatalf("interval rollover frames=%d TCA=%d alarms=%d, want one clear",
			len(frames), len(protocol.performanceTCA), len(protocol.alarms))
	}
	alarm = decodeResponse(t, frames[0]).Layer(omci.LayerTypeAlarmNotification).(*omci.AlarmNotificationMsg)
	if alarm.AlarmBitmap != ([28]byte{}) || alarm.AlarmSequenceNumber != 3 {
		t.Fatalf("interval rollover clear TCA = %#v", alarm)
	}
	controller.ethernetCounters.Received.DropEvents = 111
	frames = pollPerformance(t, protocol)
	if len(frames) != 1 {
		t.Fatalf("next-interval TCA frames = %d, want 1", len(frames))
	}
	alarm = decodeResponse(t, frames[0]).Layer(omci.LayerTypeAlarmNotification).(*omci.AlarmNotificationMsg)
	if alarm.AlarmBitmap[0] != 0x80 || alarm.AlarmSequenceNumber != 4 {
		t.Fatalf("next-interval TCA = %#v", alarm)
	}
}

func TestPerformanceThresholdFFFFDisablesTCA(t *testing.T) {
	protocol, store, controller := newEthernetPerformanceEngine(t, false)
	thresholdID := uint16(0x915)
	if err := store.Create(me.ThresholdData1ClassID, thresholdID, me.AttributeValueMap{
		me.ThresholdData1_ThresholdValue1: uint32(0xffff),
	}); err != nil {
		t.Fatalf("Create(disabled ThresholdData1) error = %v", err)
	}
	controller.ethernetCounters.Received.DropEvents = 10
	createEthernetPerformanceWithThreshold(t, protocol,
		me.EthernetPerformanceMonitoringHistoryData3ClassID, testGEMEntity, thresholdID, 0x648)
	pollPerformance(t, protocol)

	controller.ethernetCounters.Received.DropEvents = 0x10010
	if frames := pollPerformance(t, protocol); len(frames) != 0 {
		t.Fatalf("disabled threshold TCA frames = %d, want 0", len(frames))
	}
	if controller.ethernetCalls != 1 {
		t.Fatalf("disabled threshold hardware samples = %d, want Create-only sample",
			controller.ethernetCalls)
	}
}

func TestPerformanceThresholdCrossedAtBoundaryRaisesThenClears(t *testing.T) {
	protocol, store, controller := newEthernetPerformanceEngine(t, false)
	now := time.Date(2026, 8, 11, 5, 30, 0, 0, time.UTC)
	protocol.now = func() time.Time { return now }
	thresholdID := uint16(0x918)
	if err := store.Create(me.ThresholdData1ClassID, thresholdID, me.AttributeValueMap{
		me.ThresholdData1_ThresholdValue1: uint32(5),
	}); err != nil {
		t.Fatalf("Create(boundary ThresholdData1) error = %v", err)
	}
	createEthernetPerformanceWithThreshold(t, protocol,
		me.EthernetPerformanceMonitoringHistoryData3ClassID, testGEMEntity, thresholdID, 0x649)
	pollPerformance(t, protocol)

	now = now.Add(performanceInterval)
	controller.ethernetCounters.Received.DropEvents = 5
	frames := pollPerformance(t, protocol)
	if len(frames) != 2 {
		t.Fatalf("boundary crossing TCA frames = %d, want raise and clear", len(frames))
	}
	raised := decodeResponse(t, frames[0]).Layer(omci.LayerTypeAlarmNotification).(*omci.AlarmNotificationMsg)
	cleared := decodeResponse(t, frames[1]).Layer(omci.LayerTypeAlarmNotification).(*omci.AlarmNotificationMsg)
	if raised.AlarmBitmap[0] != 0x80 || raised.AlarmSequenceNumber != 1 ||
		cleared.AlarmBitmap != ([28]byte{}) || cleared.AlarmSequenceNumber != 2 {
		t.Fatalf("boundary crossing raised=%#v cleared=%#v", raised, cleared)
	}
}

func TestEthernetPerformanceThresholdData2Mapping(t *testing.T) {
	protocol, store, controller := newEthernetPerformanceEngine(t, false)
	now := time.Date(2026, 8, 11, 6, 0, 0, 0, time.UTC)
	protocol.now = func() time.Time { return now }
	thresholdID := uint16(0x920)
	if err := store.Create(me.ThresholdData1ClassID, thresholdID, me.AttributeValueMap{}); err != nil {
		t.Fatalf("Create(ThresholdData1 for TD2) error = %v", err)
	}
	if err := store.Create(me.ThresholdData2ClassID, thresholdID, me.AttributeValueMap{
		me.ThresholdData2_ThresholdValue11: uint32(3),
	}); err != nil {
		t.Fatalf("Create(ThresholdData2) error = %v", err)
	}
	controller.ethernetCounters.Transmitted.InternalErrors = 10
	createEthernetPerformanceWithThreshold(t, protocol,
		me.EthernetPerformanceMonitoringHistoryDataClassID, testGEMEntity, thresholdID, 0x650)
	pollPerformance(t, protocol)
	controller.ethernetCounters.Transmitted.InternalErrors = 13
	frames := pollPerformance(t, protocol)
	if len(frames) != 1 {
		t.Fatalf("Threshold Data 2 TCA frames = %d, want 1", len(frames))
	}
	alarm := decodeResponse(t, frames[0]).Layer(omci.LayerTypeAlarmNotification).(*omci.AlarmNotificationMsg)
	if alarm.AlarmBitmap[1] != 0x20 || alarm.AlarmSequenceNumber != 1 {
		t.Fatalf("Threshold Data 2 TCA = %#v", alarm)
	}
}

func TestPerformanceThresholdSetDoesNotRepeatActiveTCA(t *testing.T) {
	protocol, store, controller := newEthernetPerformanceEngine(t, false)
	thresholdID := uint16(0x930)
	if err := store.Create(me.ThresholdData1ClassID, thresholdID, me.AttributeValueMap{
		me.ThresholdData1_ThresholdValue1: uint32(2),
	}); err != nil {
		t.Fatalf("Create(active ThresholdData1) error = %v", err)
	}
	createEthernetPerformanceWithThreshold(t, protocol,
		me.EthernetPerformanceMonitoringHistoryData3ClassID, testGEMEntity, thresholdID, 0x660)
	pollPerformance(t, protocol)
	controller.ethernetCounters.Received.DropEvents = 2
	if frames := pollPerformance(t, protocol); len(frames) != 1 {
		t.Fatalf("initial TCA frames = %d, want 1", len(frames))
	}

	setThreshold := encodeRequest(t, 0x661, omci.SetRequestType, &omci.SetRequest{
		MeBasePacket: omci.MeBasePacket{
			EntityClass: me.ThresholdData1ClassID, EntityInstance: thresholdID,
		},
		AttributeMask: 0x8000,
		Attributes: me.AttributeValueMap{
			me.ThresholdData1_ThresholdValue1: uint32(3),
		},
	})
	encoded, err := protocol.Handle(setThreshold)
	if err != nil {
		t.Fatalf("Handle(Set ThresholdData1) error = %v", err)
	}
	response := decodeResponse(t, encoded).Layer(omci.LayerTypeSetResponse).(*omci.SetResponse)
	if response.Result != me.Success || len(protocol.performanceTCA) != 1 || len(protocol.alarms) != 1 {
		t.Fatalf("Set threshold result=%v TCA=%d alarms=%d", response.Result,
			len(protocol.performanceTCA), len(protocol.alarms))
	}
	if frames := pollPerformance(t, protocol); len(frames) != 0 {
		t.Fatalf("below updated threshold TCA frames = %d, want 0", len(frames))
	}
	controller.ethernetCounters.Received.DropEvents = 3
	frames := pollPerformance(t, protocol)
	if len(frames) != 0 {
		t.Fatalf("repeated active TCA frames = %d, want 0", len(frames))
	}
}

func TestSynchronizeTimeAtomicallyRestartsPerformanceIntervals(t *testing.T) {
	protocol, store, controller := newPerformanceEngine(t)
	start := time.Date(2026, 8, 11, 1, 0, 0, 0, time.UTC)
	protocol.now = func() time.Time { return start }
	protocol.controller = controller
	controller.counters = performance.GEMPortCounters{ReceivedGEMFrames: 100}
	pollPerformance(t, protocol)
	controller.counters.ReceivedGEMFrames = 150
	active := [28]byte{0x40}
	protocol.performanceTCA[gemPerformanceKey()] = active
	protocol.alarms[gemPerformanceKey()] = active

	requested := time.Date(2026, 8, 11, 2, 3, 4, 0, time.UTC)
	request := encodeRequest(t, 0x620, omci.SynchronizeTimeRequestType, &omci.SynchronizeTimeRequest{
		MeBasePacket: omci.MeBasePacket{EntityClass: me.OnuGClassID, EntityInstance: 0},
		Year:         uint16(requested.Year()), Month: uint8(requested.Month()), Day: uint8(requested.Day()),
		Hour: uint8(requested.Hour()), Minute: uint8(requested.Minute()), Second: uint8(requested.Second()),
	})
	encoded, err := protocol.Handle(request)
	if err != nil {
		t.Fatalf("Handle(SynchronizeTime) error = %v", err)
	}
	if me.Results(encoded[8]) != me.Success || !controller.synced.Equal(requested) {
		t.Fatalf("SynchronizeTime result=%v time=%v", me.Results(encoded[8]), controller.synced)
	}
	frames := protocol.DrainNotifications()
	if len(frames) != 1 {
		t.Fatalf("SynchronizeTime clear TCA frames = %d, want 1", len(frames))
	}
	alarm := decodeResponse(t, frames[0]).Layer(omci.LayerTypeAlarmNotification).(*omci.AlarmNotificationMsg)
	if alarm.AlarmBitmap != ([28]byte{}) || alarm.AlarmSequenceNumber != 1 ||
		len(protocol.performanceTCA) != 0 || len(protocol.alarms) != 0 {
		t.Fatalf("SynchronizeTime clear TCA = %#v TCA=%d alarms=%d", alarm,
			len(protocol.performanceTCA), len(protocol.alarms))
	}
	history, err := store.Get(gemPerformanceKey(), 0x9000)
	if err != nil {
		t.Fatalf("Get(reset history) error = %v", err)
	}
	if history.Attributes[me.GemPortNetworkCtpPerformanceMonitoringHistoryData_IntervalEndTime] != uint8(0) ||
		history.Attributes[me.GemPortNetworkCtpPerformanceMonitoringHistoryData_ReceivedGemFrames] != uint32(0) {
		t.Fatalf("reset history = %#v", history.Attributes)
	}
	controller.counters.ReceivedGEMFrames = 175
	current := getCurrentGEMPerformance(t, protocol, 0x1000)
	if current.Result != me.Success ||
		current.Attributes[me.GemPortNetworkCtpPerformanceMonitoringHistoryData_ReceivedGemFrames] != uint32(25) {
		t.Fatalf("post-sync current = %#v", current)
	}
}

func newPerformanceEngine(t *testing.T) (*Engine, *mib.Store, *recordingPerformanceController) {
	t.Helper()
	store, err := mib.New([]mib.Instance{
		{
			Key:        mib.Key{ClassID: me.OnuDataClassID, EntityID: 0},
			Attributes: me.AttributeValueMap{me.OnuData_MibDataSync: uint8(0)},
		},
		{
			Key:        mib.Key{ClassID: me.GemPortNetworkCtpClassID, EntityID: testGEMEntity},
			Attributes: me.AttributeValueMap{me.GemPortNetworkCtp_PortId: testGEMPort},
		},
		{
			Key: gemPerformanceKey(),
			Attributes: me.AttributeValueMap{
				me.GemPortNetworkCtpPerformanceMonitoringHistoryData_IntervalEndTime:         uint8(0),
				me.GemPortNetworkCtpPerformanceMonitoringHistoryData_ThresholdData12Id:       uint16(0),
				me.GemPortNetworkCtpPerformanceMonitoringHistoryData_TransmittedGemFrames:    uint32(0),
				me.GemPortNetworkCtpPerformanceMonitoringHistoryData_ReceivedGemFrames:       uint32(0),
				me.GemPortNetworkCtpPerformanceMonitoringHistoryData_ReceivedPayloadBytes:    uint64(0),
				me.GemPortNetworkCtpPerformanceMonitoringHistoryData_TransmittedPayloadBytes: uint64(0),
				me.GemPortNetworkCtpPerformanceMonitoringHistoryData_EncryptionKeyErrors:     uint32(0),
			},
		},
	})
	if err != nil {
		t.Fatalf("mib.New() error = %v", err)
	}
	controller := &recordingPerformanceController{}
	protocol := New(store)
	protocol.SetPerformanceController(controller)
	return protocol, store, controller
}

func newTestStoreForPerformanceCreate(t *testing.T) *mib.Store {
	t.Helper()
	store, err := mib.New([]mib.Instance{{
		Key:        mib.Key{ClassID: me.OnuDataClassID, EntityID: 0},
		Attributes: me.AttributeValueMap{me.OnuData_MibDataSync: uint8(0)},
	}})
	if err != nil {
		t.Fatalf("mib.New() error = %v", err)
	}
	if err := store.Create(me.GemPortNetworkCtpClassID, testGEMEntity, me.AttributeValueMap{
		me.GemPortNetworkCtp_PortId: testGEMPort,
	}); err != nil {
		t.Fatalf("Create(GEM parent) error = %v", err)
	}
	return store
}

func createGEMPerformanceRequest(t *testing.T, transactionID uint16) []byte {
	return createGEMPerformanceRequestWithThreshold(t, transactionID, 0)
}

func createGEMPerformanceRequestWithThreshold(t *testing.T, transactionID, thresholdID uint16) []byte {
	t.Helper()
	return encodeRequest(t, transactionID, omci.CreateRequestType, &omci.CreateRequest{
		MeBasePacket: omci.MeBasePacket{
			EntityClass:    me.GemPortNetworkCtpPerformanceMonitoringHistoryDataClassID,
			EntityInstance: testGEMEntity,
		},
		Attributes: me.AttributeValueMap{
			me.GemPortNetworkCtpPerformanceMonitoringHistoryData_ThresholdData12Id: thresholdID,
		},
	})
}

func gemPerformanceKey() mib.Key {
	return mib.Key{
		ClassID:  me.GemPortNetworkCtpPerformanceMonitoringHistoryDataClassID,
		EntityID: testGEMEntity,
	}
}

func getCurrentGEMPerformance(t *testing.T, protocol *Engine, mask uint16) *omci.GetCurrentDataResponse {
	t.Helper()
	request := encodeRequest(t, uint16(0x500+mask), omci.GetCurrentDataRequestType,
		&omci.GetCurrentDataRequest{
			MeBasePacket: omci.MeBasePacket{
				EntityClass:    me.GemPortNetworkCtpPerformanceMonitoringHistoryDataClassID,
				EntityInstance: testGEMEntity,
			},
			AttributeMask: mask,
		})
	encoded, err := protocol.Handle(request)
	if err != nil {
		t.Fatalf("Handle(GetCurrentData %#x) error = %v", mask, err)
	}
	return decodeResponse(t, encoded).Layer(omci.LayerTypeGetCurrentDataResponse).(*omci.GetCurrentDataResponse)
}

func newEthernetPerformanceEngine(t *testing.T,
	withBridgePort bool) (*Engine, *mib.Store, *recordingPerformanceController) {
	t.Helper()
	factory := []mib.Instance{
		{
			Key:        mib.Key{ClassID: me.OnuDataClassID, EntityID: 0},
			Attributes: me.AttributeValueMap{me.OnuData_MibDataSync: uint8(0)},
		},
		{
			Key: mib.Key{
				ClassID:  me.PhysicalPathTerminationPointEthernetUniClassID,
				EntityID: testGEMEntity,
			},
			Attributes: me.AttributeValueMap{
				me.PhysicalPathTerminationPointEthernetUni_AdministrativeState: uint8(0),
			},
		},
		{
			Key: mib.Key{
				ClassID:  me.PhysicalPathTerminationPointEthernetUniClassID,
				EntityID: 0x0102,
			},
			Attributes: me.AttributeValueMap{
				me.PhysicalPathTerminationPointEthernetUni_AdministrativeState: uint8(0),
			},
		},
	}
	if withBridgePort {
		factory = append(factory, mib.Instance{
			Key: mib.Key{
				ClassID:  me.MacBridgePortConfigurationDataClassID,
				EntityID: 0x0201,
			},
			Attributes: me.AttributeValueMap{
				me.MacBridgePortConfigurationData_TpType:    uint8(1),
				me.MacBridgePortConfigurationData_TpPointer: testGEMEntity,
			},
		})
	}
	store, err := mib.New(factory)
	if err != nil {
		t.Fatalf("mib.New(Ethernet performance) error = %v", err)
	}
	controller := &recordingPerformanceController{}
	protocol := New(store)
	protocol.SetPerformanceController(controller)
	return protocol, store, controller
}

func createEthernetPerformance(t *testing.T, protocol *Engine,
	classID me.ClassID, entityID, transactionID uint16) {
	createEthernetPerformanceWithThreshold(t, protocol, classID, entityID, 0, transactionID)
}

func createEthernetPerformanceWithThreshold(t *testing.T, protocol *Engine,
	classID me.ClassID, entityID, thresholdID, transactionID uint16) {
	t.Helper()
	request := encodeRequest(t, transactionID, omci.CreateRequestType, &omci.CreateRequest{
		MeBasePacket: omci.MeBasePacket{
			EntityClass: classID, EntityInstance: entityID,
		},
		Attributes: me.AttributeValueMap{"ThresholdData12Id": thresholdID},
	})
	encoded, err := protocol.Handle(request)
	if err != nil {
		t.Fatalf("Handle(Create Ethernet PM %v/%#x) error = %v", classID, entityID, err)
	}
	response := decodeResponse(t, encoded).Layer(omci.LayerTypeCreateResponse).(*omci.CreateResponse)
	if response.Result != me.Success {
		t.Fatalf("Create Ethernet PM %v/%#x result = %v", classID, entityID, response.Result)
	}
}

func getCurrentPerformance(t *testing.T, protocol *Engine, classID me.ClassID,
	entityID, mask, transactionID uint16) *omci.GetCurrentDataResponse {
	t.Helper()
	request := encodeRequest(t, transactionID, omci.GetCurrentDataRequestType,
		&omci.GetCurrentDataRequest{
			MeBasePacket: omci.MeBasePacket{
				EntityClass: classID, EntityInstance: entityID,
			},
			AttributeMask: mask,
		})
	encoded, err := protocol.Handle(request)
	if err != nil {
		t.Fatalf("Handle(GetCurrentData %v/%#x) error = %v", classID, entityID, err)
	}
	return decodeResponse(t, encoded).Layer(omci.LayerTypeGetCurrentDataResponse).(*omci.GetCurrentDataResponse)
}

func pollPerformance(t *testing.T, protocol *Engine) [][]byte {
	t.Helper()
	frames, err := protocol.PollPerformance(omci.BaselineIdent)
	if err != nil {
		t.Fatalf("PollPerformance() error = %v", err)
	}
	return frames
}
