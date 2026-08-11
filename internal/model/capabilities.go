// SPDX-License-Identifier: Apache-2.0

package model

import (
	"sort"

	me "github.com/opencord/omci-lib-go/v2/generated"
)

// XG2010GSupportedClasses is the authoritative list of managed-entity classes
// accepted by this ONU implementation. A generated G.988 definition alone is
// not evidence that the XG2010G backend implements the class.
func XG2010GSupportedClasses() []me.ClassID {
	classes := []me.ClassID{
		me.OnuDataClassID,
		me.CircuitPackClassID,
		me.SoftwareImageClassID,
		me.PhysicalPathTerminationPointEthernetUniClassID,
		me.EthernetPerformanceMonitoringHistoryDataClassID,
		me.MacBridgeServiceProfileClassID,
		me.MacBridgePortConfigurationDataClassID,
		me.VlanTaggingOperationConfigurationDataClassID,
		me.VlanTaggingFilterDataClassID,
		me.Ieee8021PMapperServiceProfileClassID,
		me.ExtendedVlanTaggingOperationConfigurationDataClassID,
		me.OnuGClassID,
		me.Onu2GClassID,
		me.TContClassID,
		me.AniGClassID,
		me.UniGClassID,
		me.GemInterworkingTerminationPointClassID,
		me.GemPortNetworkCtpClassID,
		me.GalEthernetProfileClassID,
		me.PriorityQueueClassID,
		me.TrafficSchedulerClassID,
		me.TrafficDescriptorClassID,
		me.Dot1RateLimiterClassID,
		me.MulticastGemInterworkingTerminationPointClassID,
		me.EthernetPerformanceMonitoringHistoryData3ClassID,
		me.MulticastOperationsProfileClassID,
		me.MulticastSubscriberConfigInfoClassID,
		me.MulticastSubscriberMonitorClassID,
		me.EthernetFramePerformanceMonitoringHistoryDataDownstreamClassID,
		me.EthernetFramePerformanceMonitoringHistoryDataUpstreamClassID,
		me.OmciClassID,
		me.ManagedEntityMeClassID,
		me.AttributeMeClassID,
		me.ThresholdData1ClassID,
		me.ThresholdData2ClassID,
		me.FecPerformanceMonitoringHistoryDataClassID,
		me.GemPortNetworkCtpPerformanceMonitoringHistoryDataClassID,
	}
	sort.Slice(classes, func(i, j int) bool { return classes[i] < classes[j] })
	return classes
}
