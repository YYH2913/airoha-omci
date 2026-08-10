// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"fmt"
	"math"
	"time"

	me "github.com/opencord/omci-lib-go/v2/generated"
	"github.com/xg2010g/airoha-omci/internal/mib"
	"github.com/xg2010g/airoha-omci/internal/performance"
)

const performanceInterval = 15 * time.Minute

type performanceState struct {
	portID   uint16
	baseline performance.GEMPortCounters
}

type ethernetPerformanceState struct {
	uniEntityID uint16
	baseline    performance.EthernetCounters
}

type performanceSynchronization struct {
	gem      map[mib.Key]performance.GEMPortCounters
	ethernet map[mib.Key]performance.EthernetCounters
}

func isEthernetPerformanceClass(classID me.ClassID) bool {
	switch classID {
	case me.EthernetPerformanceMonitoringHistoryDataClassID,
		me.EthernetPerformanceMonitoringHistoryData3ClassID,
		me.EthernetFramePerformanceMonitoringHistoryDataDownstreamClassID,
		me.EthernetFramePerformanceMonitoringHistoryDataUpstreamClassID:
		return true
	default:
		return false
	}
}

func (e *Engine) resolveEthernetPerformanceUNILocked(key mib.Key) (uint16, error) {
	uniEntityID := key.EntityID
	switch key.ClassID {
	case me.EthernetPerformanceMonitoringHistoryDataClassID,
		me.EthernetPerformanceMonitoringHistoryData3ClassID:
	case me.EthernetFramePerformanceMonitoringHistoryDataDownstreamClassID,
		me.EthernetFramePerformanceMonitoringHistoryDataUpstreamClassID:
		bridgePort, err := e.mib.Get(mib.Key{
			ClassID: me.MacBridgePortConfigurationDataClassID, EntityID: key.EntityID,
		}, 0x3000)
		if err != nil {
			return 0, fmt.Errorf("Ethernet frame PM parent: %w", err)
		}
		tpType, ok := bridgePort.Attributes[me.MacBridgePortConfigurationData_TpType].(uint8)
		if !ok || tpType != 1 {
			return 0, fmt.Errorf("Ethernet frame PM requires a PPTP Ethernet UNI bridge port")
		}
		var present bool
		uniEntityID, present = bridgePort.Attributes[me.MacBridgePortConfigurationData_TpPointer].(uint16)
		if !present {
			return 0, fmt.Errorf("Ethernet frame PM parent has an invalid TP pointer")
		}
	default:
		return 0, fmt.Errorf("unsupported Ethernet performance class %v", key.ClassID)
	}
	if !e.mib.Exists(mib.Key{
		ClassID: me.PhysicalPathTerminationPointEthernetUniClassID, EntityID: uniEntityID,
	}) {
		return 0, fmt.Errorf("Ethernet PM references missing PPTP Ethernet UNI %#x", uniEntityID)
	}
	return uniEntityID, nil
}

func (e *Engine) prepareEthernetPerformanceCreateLocked(classID me.ClassID,
	entityID uint16) (*ethernetPerformanceState, error) {
	key := mib.Key{ClassID: classID, EntityID: entityID}
	if e.mib.Exists(key) {
		return nil, nil
	}
	if e.ethernetPerformance == nil {
		return nil, &mib.ResultError{Result: me.NotSupported}
	}
	uniEntityID, err := e.resolveEthernetPerformanceUNILocked(key)
	if err != nil {
		return nil, &mib.ResultError{Result: me.ParameterError, Cause: err}
	}
	baseline, err := e.ethernetPerformance.EthernetCounters(uniEntityID)
	if err != nil {
		return nil, &mib.ResultError{Result: me.ProcessingError, Cause: err}
	}
	return &ethernetPerformanceState{uniEntityID: uniEntityID, baseline: baseline}, nil
}

func (e *Engine) bridgePortHasPerformanceLocked(entityID uint16) bool {
	return e.mib.Exists(mib.Key{
		ClassID:  me.EthernetFramePerformanceMonitoringHistoryDataDownstreamClassID,
		EntityID: entityID,
	}) || e.mib.Exists(mib.Key{
		ClassID:  me.EthernetFramePerformanceMonitoringHistoryDataUpstreamClassID,
		EntityID: entityID,
	})
}

func bridgePortAssociationChanged(attributes me.AttributeValueMap) bool {
	_, typeChanged := attributes[me.MacBridgePortConfigurationData_TpType]
	_, pointerChanged := attributes[me.MacBridgePortConfigurationData_TpPointer]
	return typeChanged || pointerChanged
}

func (e *Engine) prepareGEMPerformanceCreateLocked(entityID uint16) (*performanceState, error) {
	key := mib.Key{
		ClassID:  me.GemPortNetworkCtpPerformanceMonitoringHistoryDataClassID,
		EntityID: entityID,
	}
	if e.mib.Exists(key) {
		return nil, nil
	}
	if e.performance == nil {
		return nil, &mib.ResultError{Result: me.NotSupported}
	}
	parent, err := e.mib.Get(mib.Key{
		ClassID: me.GemPortNetworkCtpClassID, EntityID: entityID,
	}, 0x8000)
	if err != nil {
		return nil, &mib.ResultError{Result: me.ParameterError, Cause: fmt.Errorf("GEM PM parent: %w", err)}
	}
	portID, ok := parent.Attributes[me.GemPortNetworkCtp_PortId].(uint16)
	if !ok || portID > 0x0fff {
		return nil, &mib.ResultError{Result: me.ParameterError, Cause: fmt.Errorf("GEM PM parent has invalid port ID")}
	}
	baseline, err := e.performance.GEMPortCounters(portID)
	if err != nil {
		return nil, &mib.ResultError{Result: me.ProcessingError, Cause: err}
	}
	return &performanceState{portID: portID, baseline: baseline}, nil
}

// SetPerformanceController installs the platform counter source. It is mainly
// useful to embedders that do not use the combined platform controller.
func (e *Engine) SetPerformanceController(controller performance.Controller) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.performance = controller
	e.performanceState = make(map[mib.Key]performanceState)
	e.ethernetPerformance, _ = controller.(performance.EthernetController)
	e.ethernetPMState = make(map[mib.Key]ethernetPerformanceState)
	e.performanceNext = time.Time{}
}

// PollPerformance advances completed 15-minute collection intervals. The
// daemon calls it periodically; hardware is sampled only for a new PM instance
// or when an interval boundary has passed.
func (e *Engine) PollPerformance() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pollPerformanceLocked(e.now())
}

func (e *Engine) pollPerformanceLocked(now time.Time) error {
	if e.performance == nil && e.ethernetPerformance == nil {
		return nil
	}
	if err := e.reconcilePerformanceLocked(); err != nil {
		return err
	}
	if e.performanceNext.IsZero() {
		e.performanceNext = now.Add(performanceInterval)
		return nil
	}
	if now.Before(e.performanceNext) {
		return nil
	}

	intervals := uint64(now.Sub(e.performanceNext)/performanceInterval) + 1
	intervalEnd := e.performanceIntervalEnd + uint8(intervals)
	updates := make(map[mib.Key]me.AttributeValueMap,
		len(e.performanceState)+len(e.ethernetPMState))
	baselines := make(map[mib.Key]performance.GEMPortCounters, len(e.performanceState))
	for key, state := range e.performanceState {
		current, err := e.performance.GEMPortCounters(state.portID)
		if err != nil {
			return fmt.Errorf("read GEM port %d performance counters: %w", state.portID, err)
		}
		attributes := zeroGEMPerformanceAttributes(intervalEnd)
		if intervals == 1 {
			attributes = gemPerformanceAttributes(intervalEnd,
				deltaGEMCounters(state.baseline, current))
		}
		updates[key] = attributes
		baselines[key] = current
	}
	ethernetBaselines := make(map[mib.Key]performance.EthernetCounters, len(e.ethernetPMState))
	for key, state := range e.ethernetPMState {
		current, err := e.ethernetPerformance.EthernetCounters(state.uniEntityID)
		if err != nil {
			return fmt.Errorf("read Ethernet UNI %#x performance counters: %w",
				state.uniEntityID, err)
		}
		attributes := ethernetPerformanceAttributes(key.ClassID, intervalEnd,
			performance.EthernetCounters{})
		if intervals == 1 {
			attributes = ethernetPerformanceAttributes(key.ClassID, intervalEnd,
				deltaEthernetCounters(state.baseline, current))
		}
		updates[key] = attributes
		ethernetBaselines[key] = current
	}
	if err := e.mib.UpdateAutonomousBatch(updates); err != nil {
		return fmt.Errorf("update performance history: %w", err)
	}
	for key, baseline := range baselines {
		state := e.performanceState[key]
		state.baseline = baseline
		e.performanceState[key] = state
	}
	for key, baseline := range ethernetBaselines {
		state := e.ethernetPMState[key]
		state.baseline = baseline
		e.ethernetPMState[key] = state
	}
	e.performanceIntervalEnd = intervalEnd
	e.performanceNext = e.performanceNext.Add(time.Duration(intervals) * performanceInterval)
	return nil
}

func (e *Engine) reconcilePerformanceLocked() error {
	activeGEM := make(map[mib.Key]struct{})
	for _, instance := range e.mib.Snapshot() {
		if instance.ClassID == me.GemPortNetworkCtpPerformanceMonitoringHistoryDataClassID &&
			e.performance != nil {
			key := instance.Key
			activeGEM[key] = struct{}{}
			parent, err := e.mib.Get(mib.Key{
				ClassID: me.GemPortNetworkCtpClassID, EntityID: key.EntityID,
			}, 0x8000)
			if err != nil {
				return fmt.Errorf("resolve GEM PM parent %#x: %w", key.EntityID, err)
			}
			portID, ok := parent.Attributes[me.GemPortNetworkCtp_PortId].(uint16)
			if !ok || portID > 0x0fff {
				return fmt.Errorf("GEM PM parent %#x has invalid port ID", key.EntityID)
			}
			state, present := e.performanceState[key]
			if present && state.portID == portID {
				continue
			}
			baseline, err := e.performance.GEMPortCounters(portID)
			if err != nil {
				return fmt.Errorf("initialize GEM port %d performance counters: %w", portID, err)
			}
			e.performanceState[key] = performanceState{portID: portID, baseline: baseline}
		}
	}
	for key := range e.performanceState {
		if _, present := activeGEM[key]; !present {
			delete(e.performanceState, key)
		}
	}

	activeEthernet := make(map[mib.Key]struct{})
	for _, instance := range e.mib.Snapshot() {
		if !isEthernetPerformanceClass(instance.ClassID) || e.ethernetPerformance == nil {
			continue
		}
		key := instance.Key
		activeEthernet[key] = struct{}{}
		uniEntityID, err := e.resolveEthernetPerformanceUNILocked(key)
		if err != nil {
			return fmt.Errorf("resolve Ethernet PM %v/%#x: %w", key.ClassID, key.EntityID, err)
		}
		state, present := e.ethernetPMState[key]
		if present && state.uniEntityID == uniEntityID {
			continue
		}
		baseline, err := e.ethernetPerformance.EthernetCounters(uniEntityID)
		if err != nil {
			return fmt.Errorf("initialize Ethernet UNI %#x performance counters: %w",
				uniEntityID, err)
		}
		e.ethernetPMState[key] = ethernetPerformanceState{
			uniEntityID: uniEntityID, baseline: baseline,
		}
	}
	for key := range e.ethernetPMState {
		if _, present := activeEthernet[key]; !present {
			delete(e.ethernetPMState, key)
		}
	}
	return nil
}

func (e *Engine) getCurrentPerformanceLocked(key mib.Key, mask uint16) (mib.Instance, error) {
	gemPerformance := key.ClassID == me.GemPortNetworkCtpPerformanceMonitoringHistoryDataClassID
	ethernetPerformance := isEthernetPerformanceClass(key.ClassID)
	if (!gemPerformance || e.performance == nil) &&
		(!ethernetPerformance || e.ethernetPerformance == nil) {
		return mib.Instance{}, &mib.ResultError{Result: me.NotSupported}
	}
	if currentDataSize(key.ClassID, mask) > 25 {
		return mib.Instance{}, &mib.ResultError{Result: me.ParameterError}
	}
	if err := e.reconcilePerformanceLocked(); err != nil {
		return mib.Instance{}, &mib.ResultError{Result: me.ProcessingError, Cause: err}
	}
	instance, getErr := e.mib.Get(key, mask)
	if instance.Attributes == nil {
		return instance, getErr
	}
	var values me.AttributeValueMap
	if gemPerformance {
		state, present := e.performanceState[key]
		if !present {
			return mib.Instance{}, &mib.ResultError{Result: me.UnknownInstance}
		}
		current, err := e.performance.GEMPortCounters(state.portID)
		if err != nil {
			return mib.Instance{}, &mib.ResultError{Result: me.ProcessingError, Cause: err}
		}
		values = gemPerformanceAttributes(e.performanceIntervalEnd,
			deltaGEMCounters(state.baseline, current))
	} else {
		state, present := e.ethernetPMState[key]
		if !present {
			return mib.Instance{}, &mib.ResultError{Result: me.UnknownInstance}
		}
		current, err := e.ethernetPerformance.EthernetCounters(state.uniEntityID)
		if err != nil {
			return mib.Instance{}, &mib.ResultError{Result: me.ProcessingError, Cause: err}
		}
		values = ethernetPerformanceAttributes(key.ClassID, e.performanceIntervalEnd,
			deltaEthernetCounters(state.baseline, current))
	}
	for name := range instance.Attributes {
		if value, dynamic := values[name]; dynamic {
			instance.Attributes[name] = value
		}
	}
	return instance, getErr
}

func (e *Engine) preparePerformanceSynchronizationLocked() (performanceSynchronization, error) {
	if e.performance == nil && e.ethernetPerformance == nil {
		return performanceSynchronization{}, nil
	}
	if err := e.reconcilePerformanceLocked(); err != nil {
		return performanceSynchronization{}, err
	}
	baselines := performanceSynchronization{
		gem:      make(map[mib.Key]performance.GEMPortCounters, len(e.performanceState)),
		ethernet: make(map[mib.Key]performance.EthernetCounters, len(e.ethernetPMState)),
	}
	for key, state := range e.performanceState {
		current, err := e.performance.GEMPortCounters(state.portID)
		if err != nil {
			return performanceSynchronization{}, fmt.Errorf(
				"synchronize GEM port %d performance counters: %w", state.portID, err)
		}
		baselines.gem[key] = current
	}
	for key, state := range e.ethernetPMState {
		current, err := e.ethernetPerformance.EthernetCounters(state.uniEntityID)
		if err != nil {
			return performanceSynchronization{}, fmt.Errorf(
				"synchronize Ethernet UNI %#x performance counters: %w",
				state.uniEntityID, err)
		}
		baselines.ethernet[key] = current
	}
	return baselines, nil
}

func (e *Engine) commitPerformanceSynchronizationLocked(now time.Time,
	baselines performanceSynchronization) error {
	updates := make(map[mib.Key]me.AttributeValueMap,
		len(baselines.gem)+len(baselines.ethernet))
	for key := range baselines.gem {
		updates[key] = zeroGEMPerformanceAttributes(0)
	}
	for key := range baselines.ethernet {
		updates[key] = ethernetPerformanceAttributes(key.ClassID, 0,
			performance.EthernetCounters{})
	}
	if err := e.mib.UpdateAutonomousBatch(updates); err != nil {
		return err
	}
	for key, baseline := range baselines.gem {
		state := e.performanceState[key]
		state.baseline = baseline
		e.performanceState[key] = state
	}
	for key, baseline := range baselines.ethernet {
		state := e.ethernetPMState[key]
		state.baseline = baseline
		e.ethernetPMState[key] = state
	}
	e.performanceIntervalEnd = 0
	e.performanceNext = now.Add(performanceInterval)
	return nil
}

func currentDataSize(classID me.ClassID, mask uint16) int {
	entity, omciErr := me.LoadManagedEntityDefinition(classID, me.ParamData{EntityID: 0})
	if omciErr.StatusCode() != me.Success {
		return 0
	}
	size := 0
	for index, definition := range entity.GetAttributeDefinitions() {
		if index != 0 && mask&definition.Mask != 0 {
			size += definition.GetSize()
		}
	}
	return size
}

func gemPerformanceAttributes(intervalEnd uint8, counters performance.GEMPortCounters) me.AttributeValueMap {
	return me.AttributeValueMap{
		me.GemPortNetworkCtpPerformanceMonitoringHistoryData_IntervalEndTime:         intervalEnd,
		me.GemPortNetworkCtpPerformanceMonitoringHistoryData_TransmittedGemFrames:    saturatingUint32(counters.TransmittedGEMFrames),
		me.GemPortNetworkCtpPerformanceMonitoringHistoryData_ReceivedGemFrames:       saturatingUint32(counters.ReceivedGEMFrames),
		me.GemPortNetworkCtpPerformanceMonitoringHistoryData_ReceivedPayloadBytes:    counters.ReceivedPayloadBytes,
		me.GemPortNetworkCtpPerformanceMonitoringHistoryData_TransmittedPayloadBytes: counters.TransmittedPayloadBytes,
		me.GemPortNetworkCtpPerformanceMonitoringHistoryData_EncryptionKeyErrors:     uint32(0),
	}
}

func zeroGEMPerformanceAttributes(intervalEnd uint8) me.AttributeValueMap {
	return gemPerformanceAttributes(intervalEnd, performance.GEMPortCounters{})
}

func ethernetPerformanceAttributes(classID me.ClassID, intervalEnd uint8,
	counters performance.EthernetCounters) me.AttributeValueMap {
	switch classID {
	case me.EthernetPerformanceMonitoringHistoryDataClassID:
		rx := counters.Received
		tx := counters.Transmitted
		return me.AttributeValueMap{
			me.EthernetPerformanceMonitoringHistoryData_IntervalEndTime:                 intervalEnd,
			me.EthernetPerformanceMonitoringHistoryData_FcsErrors:                       saturatingUint32(rx.CRCErrors),
			me.EthernetPerformanceMonitoringHistoryData_ExcessiveCollisionCounter:       uint32(0),
			me.EthernetPerformanceMonitoringHistoryData_LateCollisionCounter:            uint32(0),
			me.EthernetPerformanceMonitoringHistoryData_FramesTooLong:                   saturatingUint32(rx.OversizeFrames),
			me.EthernetPerformanceMonitoringHistoryData_BufferOverflowsOnReceive:        saturatingUint32(rx.BufferOverflows),
			me.EthernetPerformanceMonitoringHistoryData_BufferOverflowsOnTransmit:       saturatingUint32(tx.BufferOverflows),
			me.EthernetPerformanceMonitoringHistoryData_SingleCollisionFrameCounter:     uint32(0),
			me.EthernetPerformanceMonitoringHistoryData_MultipleCollisionsFrameCounter:  uint32(0),
			me.EthernetPerformanceMonitoringHistoryData_SqeCounter:                      uint32(0),
			me.EthernetPerformanceMonitoringHistoryData_DeferredTransmissionCounter:     uint32(0),
			me.EthernetPerformanceMonitoringHistoryData_InternalMacTransmitErrorCounter: saturatingUint32(tx.InternalErrors),
			me.EthernetPerformanceMonitoringHistoryData_CarrierSenseErrorCounter:        uint32(0),
			me.EthernetPerformanceMonitoringHistoryData_AlignmentErrorCounter:           uint32(0),
			me.EthernetPerformanceMonitoringHistoryData_InternalMacReceiveErrorCounter:  saturatingUint32(rx.InternalErrors),
		}

	case me.EthernetPerformanceMonitoringHistoryData3ClassID:
		rx := counters.Received
		return me.AttributeValueMap{
			me.EthernetPerformanceMonitoringHistoryData3_IntervalEndTime:         intervalEnd,
			me.EthernetPerformanceMonitoringHistoryData3_DropEvents:              saturatingUint32(rx.DropEvents),
			me.EthernetPerformanceMonitoringHistoryData3_Octets:                  saturatingUint32(rx.Octets),
			me.EthernetPerformanceMonitoringHistoryData3_Packets:                 saturatingUint32(rx.Frames),
			me.EthernetPerformanceMonitoringHistoryData3_BroadcastPackets:        saturatingUint32(rx.BroadcastFrames),
			me.EthernetPerformanceMonitoringHistoryData3_MulticastPackets:        saturatingUint32(rx.MulticastFrames),
			me.EthernetPerformanceMonitoringHistoryData3_UndersizePackets:        saturatingUint32(rx.UndersizeFrames),
			me.EthernetPerformanceMonitoringHistoryData3_Fragments:               saturatingUint32(rx.Fragments),
			me.EthernetPerformanceMonitoringHistoryData3_Jabbers:                 saturatingUint32(rx.Jabbers),
			me.EthernetPerformanceMonitoringHistoryData3_Packets64Octets:         saturatingUint32(rx.SizeBuckets[0]),
			me.EthernetPerformanceMonitoringHistoryData3_Packets65To127Octets:    saturatingUint32(rx.SizeBuckets[1]),
			me.EthernetPerformanceMonitoringHistoryData3_Packets128To255Octets:   saturatingUint32(rx.SizeBuckets[2]),
			me.EthernetPerformanceMonitoringHistoryData3_Packets256To511Octets:   saturatingUint32(rx.SizeBuckets[3]),
			me.EthernetPerformanceMonitoringHistoryData3_Packets512To1023Octets:  saturatingUint32(rx.SizeBuckets[4]),
			me.EthernetPerformanceMonitoringHistoryData3_Packets1024To1518Octets: saturatingUint32(rx.SizeBuckets[5]),
		}

	case me.EthernetFramePerformanceMonitoringHistoryDataDownstreamClassID:
		return ethernetFrameDownstreamAttributes(intervalEnd, counters.Transmitted)
	case me.EthernetFramePerformanceMonitoringHistoryDataUpstreamClassID:
		return ethernetFrameUpstreamAttributes(intervalEnd, counters.Received)
	default:
		return nil
	}
}

func ethernetFrameDownstreamAttributes(intervalEnd uint8,
	counters performance.EthernetDirectionCounters) me.AttributeValueMap {
	return me.AttributeValueMap{
		me.EthernetFramePerformanceMonitoringHistoryDataDownstream_IntervalEndTime:         intervalEnd,
		me.EthernetFramePerformanceMonitoringHistoryDataDownstream_DropEvents:              saturatingUint32(counters.DropEvents),
		me.EthernetFramePerformanceMonitoringHistoryDataDownstream_Octets:                  saturatingUint32(counters.Octets),
		me.EthernetFramePerformanceMonitoringHistoryDataDownstream_Packets:                 saturatingUint32(counters.Frames),
		me.EthernetFramePerformanceMonitoringHistoryDataDownstream_BroadcastPackets:        saturatingUint32(counters.BroadcastFrames),
		me.EthernetFramePerformanceMonitoringHistoryDataDownstream_MulticastPackets:        saturatingUint32(counters.MulticastFrames),
		me.EthernetFramePerformanceMonitoringHistoryDataDownstream_CrcErroredPackets:       saturatingUint32(counters.CRCErrors),
		me.EthernetFramePerformanceMonitoringHistoryDataDownstream_UndersizePackets:        saturatingUint32(counters.UndersizeFrames),
		me.EthernetFramePerformanceMonitoringHistoryDataDownstream_OversizePackets:         saturatingUint32(counters.OversizeFrames),
		me.EthernetFramePerformanceMonitoringHistoryDataDownstream_Packets64Octets:         saturatingUint32(counters.SizeBuckets[0]),
		me.EthernetFramePerformanceMonitoringHistoryDataDownstream_Packets65To127Octets:    saturatingUint32(counters.SizeBuckets[1]),
		me.EthernetFramePerformanceMonitoringHistoryDataDownstream_Packets128To255Octets:   saturatingUint32(counters.SizeBuckets[2]),
		me.EthernetFramePerformanceMonitoringHistoryDataDownstream_Packets256To511Octets:   saturatingUint32(counters.SizeBuckets[3]),
		me.EthernetFramePerformanceMonitoringHistoryDataDownstream_Packets512To1023Octets:  saturatingUint32(counters.SizeBuckets[4]),
		me.EthernetFramePerformanceMonitoringHistoryDataDownstream_Packets1024To1518Octets: saturatingUint32(counters.SizeBuckets[5]),
	}
}

func ethernetFrameUpstreamAttributes(intervalEnd uint8,
	counters performance.EthernetDirectionCounters) me.AttributeValueMap {
	return me.AttributeValueMap{
		me.EthernetFramePerformanceMonitoringHistoryDataUpstream_IntervalEndTime:         intervalEnd,
		me.EthernetFramePerformanceMonitoringHistoryDataUpstream_DropEvents:              saturatingUint32(counters.DropEvents),
		me.EthernetFramePerformanceMonitoringHistoryDataUpstream_Octets:                  saturatingUint32(counters.Octets),
		me.EthernetFramePerformanceMonitoringHistoryDataUpstream_Packets:                 saturatingUint32(counters.Frames),
		me.EthernetFramePerformanceMonitoringHistoryDataUpstream_BroadcastPackets:        saturatingUint32(counters.BroadcastFrames),
		me.EthernetFramePerformanceMonitoringHistoryDataUpstream_MulticastPackets:        saturatingUint32(counters.MulticastFrames),
		me.EthernetFramePerformanceMonitoringHistoryDataUpstream_CrcErroredPackets:       saturatingUint32(counters.CRCErrors),
		me.EthernetFramePerformanceMonitoringHistoryDataUpstream_UndersizePackets:        saturatingUint32(counters.UndersizeFrames),
		me.EthernetFramePerformanceMonitoringHistoryDataUpstream_OversizePackets:         saturatingUint32(counters.OversizeFrames),
		me.EthernetFramePerformanceMonitoringHistoryDataUpstream_Packets64Octets:         saturatingUint32(counters.SizeBuckets[0]),
		me.EthernetFramePerformanceMonitoringHistoryDataUpstream_Packets65To127Octets:    saturatingUint32(counters.SizeBuckets[1]),
		me.EthernetFramePerformanceMonitoringHistoryDataUpstream_Packets128To255Octets:   saturatingUint32(counters.SizeBuckets[2]),
		me.EthernetFramePerformanceMonitoringHistoryDataUpstream_Packets256To511Octets:   saturatingUint32(counters.SizeBuckets[3]),
		me.EthernetFramePerformanceMonitoringHistoryDataUpstream_Packets512To1023Octets:  saturatingUint32(counters.SizeBuckets[4]),
		me.EthernetFramePerformanceMonitoringHistoryDataUpstream_Packets1024To1518Octets: saturatingUint32(counters.SizeBuckets[5]),
	}
}

func deltaGEMCounters(start, current performance.GEMPortCounters) performance.GEMPortCounters {
	return performance.GEMPortCounters{
		ReceivedGEMFrames:       counterDelta(start.ReceivedGEMFrames, current.ReceivedGEMFrames),
		ReceivedPayloadBytes:    counterDelta(start.ReceivedPayloadBytes, current.ReceivedPayloadBytes),
		TransmittedGEMFrames:    counterDelta(start.TransmittedGEMFrames, current.TransmittedGEMFrames),
		TransmittedPayloadBytes: counterDelta(start.TransmittedPayloadBytes, current.TransmittedPayloadBytes),
	}
}

func deltaEthernetCounters(start, current performance.EthernetCounters) performance.EthernetCounters {
	return performance.EthernetCounters{
		Received:    deltaEthernetDirection(start.Received, current.Received),
		Transmitted: deltaEthernetDirection(start.Transmitted, current.Transmitted),
	}
}

func deltaEthernetDirection(start, current performance.EthernetDirectionCounters) performance.EthernetDirectionCounters {
	delta := performance.EthernetDirectionCounters{
		Frames:          counterDelta(start.Frames, current.Frames),
		Octets:          counterDelta(start.Octets, current.Octets),
		DropEvents:      counterDelta(start.DropEvents, current.DropEvents),
		BroadcastFrames: counterDelta(start.BroadcastFrames, current.BroadcastFrames),
		MulticastFrames: counterDelta(start.MulticastFrames, current.MulticastFrames),
		CRCErrors:       counterDelta(start.CRCErrors, current.CRCErrors),
		BufferOverflows: counterDelta(start.BufferOverflows, current.BufferOverflows),
		InternalErrors:  counterDelta(start.InternalErrors, current.InternalErrors),
		UndersizeFrames: counterDelta(start.UndersizeFrames, current.UndersizeFrames),
		Fragments:       counterDelta(start.Fragments, current.Fragments),
		Jabbers:         counterDelta(start.Jabbers, current.Jabbers),
		OversizeFrames:  counterDelta(start.OversizeFrames, current.OversizeFrames),
	}
	for index := range delta.SizeBuckets {
		delta.SizeBuckets[index] = counterDelta(start.SizeBuckets[index], current.SizeBuckets[index])
	}
	return delta
}

func counterDelta(start, current uint64) uint64 {
	if current < start {
		return current
	}
	return current - start
}

func saturatingUint32(value uint64) uint32 {
	if value > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(value)
}
