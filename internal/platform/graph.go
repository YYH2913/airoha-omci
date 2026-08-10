// SPDX-License-Identifier: Apache-2.0

package platform

import (
	"fmt"
	"sort"

	me "github.com/opencord/omci-lib-go/v2/generated"
	"github.com/xg2010g/airoha-omci/internal/mib"
	"github.com/xg2010g/airoha-omci/internal/vlan"
)

const nullPointer = 0xffff

var mapperPBitAttributes = [...]string{
	me.Ieee8021PMapperServiceProfile_InterworkTpPointerForPBitPriority0,
	me.Ieee8021PMapperServiceProfile_InterworkTpPointerForPBitPriority1,
	me.Ieee8021PMapperServiceProfile_InterworkTpPointerForPBitPriority2,
	me.Ieee8021PMapperServiceProfile_InterworkTpPointerForPBitPriority3,
	me.Ieee8021PMapperServiceProfile_InterworkTpPointerForPBitPriority4,
	me.Ieee8021PMapperServiceProfile_InterworkTpPointerForPBitPriority5,
	me.Ieee8021PMapperServiceProfile_InterworkTpPointerForPBitPriority6,
	me.Ieee8021PMapperServiceProfile_InterworkTpPointerForPBitPriority7,
}

type ServiceGraph struct {
	UNIs           []EthernetUNI     `json:"unis"`
	TCONTs         []TCONT           `json:"tconts"`
	GEMPorts       []GEMPort         `json:"gem_ports"`
	Interworking   []GEMInterworking `json:"gem_interworking"`
	Mappers        []PBitMapper      `json:"pbit_mappers"`
	Bridges        []MACBridge       `json:"bridges"`
	VLANFilters    []VLANFilter      `json:"vlan_filters"`
	VLANOperations []VLANOperation   `json:"vlan_operations"`
	ExtendedVLANs  []ExtendedVLAN    `json:"extended_vlans"`
}

type EthernetUNI struct {
	EntityID            uint16 `json:"entity_id"`
	Interface           string `json:"interface"`
	AdministrativeState uint8  `json:"administrative_state"`
	OperationalState    uint8  `json:"operational_state"`
	Configuration       uint8  `json:"configuration"`
}

type TCONT struct {
	EntityID uint16 `json:"entity_id"`
	AllocID  uint16 `json:"alloc_id"`
}

type GEMPort struct {
	EntityID        uint16 `json:"entity_id"`
	PortID          uint16 `json:"port_id"`
	TCONT           uint16 `json:"tcont"`
	AllocID         uint16 `json:"alloc_id"`
	Direction       uint8  `json:"direction"`
	UpstreamQueue   uint16 `json:"upstream_queue"`
	DownstreamQueue uint16 `json:"downstream_queue"`
	EncryptionRing  uint8  `json:"encryption_ring"`
}

type GEMInterworking struct {
	EntityID                uint16 `json:"entity_id"`
	GEMPort                 uint16 `json:"gem_port"`
	Option                  uint8  `json:"option"`
	ServiceProfile          uint16 `json:"service_profile"`
	InterworkingTermination uint16 `json:"interworking_termination"`
	GALProfile              uint16 `json:"gal_profile"`
}

type PBitMapper struct {
	EntityID            uint16    `json:"entity_id"`
	TPType              uint8     `json:"tp_type"`
	TPPointer           uint16    `json:"tp_pointer"`
	PBits               [8]uint16 `json:"pbits"`
	UnmarkedFrameOption uint8     `json:"unmarked_frame_option"`
	DefaultPBit         uint8     `json:"default_pbit"`
	DSCPToPBit          [64]uint8 `json:"dscp_to_pbit"`
}

type MACBridge struct {
	EntityID                uint16          `json:"entity_id"`
	SpanningTree            uint8           `json:"spanning_tree"`
	Learning                uint8           `json:"learning"`
	PortBridging            uint8           `json:"port_bridging"`
	Priority                uint16          `json:"priority"`
	MaxAge                  uint16          `json:"max_age_256ths"`
	HelloTime               uint16          `json:"hello_time_256ths"`
	ForwardDelay            uint16          `json:"forward_delay_256ths"`
	UnknownMACDiscard       uint8           `json:"unknown_mac_discard"`
	MACLearningDepth        uint8           `json:"mac_learning_depth"`
	DynamicFilteringAgeTime uint32          `json:"dynamic_filtering_age_time_seconds"`
	Ports                   []MACBridgePort `json:"ports"`
}

type MACBridgePort struct {
	EntityID         uint16 `json:"entity_id"`
	Port             uint8  `json:"port"`
	TPType           uint8  `json:"tp_type"`
	TP               uint16 `json:"tp"`
	Priority         uint16 `json:"priority"`
	PathCost         uint16 `json:"path_cost"`
	SpanningTree     uint8  `json:"spanning_tree"`
	OutboundTD       uint16 `json:"outbound_td"`
	InboundTD        uint16 `json:"inbound_td"`
	MACLearningDepth uint8  `json:"mac_learning_depth"`
}

type VLANFilter struct {
	EntityID         uint16              `json:"entity_id"`
	BridgePort       uint16              `json:"bridge_port"`
	ForwardOperation uint8               `json:"forward_operation"`
	TaggedAction     VLANFilterAction    `json:"tagged_action"`
	TaggedCriterion  VLANFilterCriterion `json:"tagged_criterion"`
	UntaggedAction   VLANFilterAction    `json:"untagged_action"`
	Entries          []uint16            `json:"entries"`
}

type VLANFilterAction string

const (
	VLANFilterActionBridge     VLANFilterAction = "a"
	VLANFilterActionDiscard    VLANFilterAction = "c"
	VLANFilterActionNegative   VLANFilterAction = "g"
	VLANFilterActionPositive   VLANFilterAction = "h"
	VLANFilterActionPositiveDA VLANFilterAction = "j"
)

type VLANFilterCriterion string

const (
	VLANFilterCriterionNone     VLANFilterCriterion = "none"
	VLANFilterCriterionVID      VLANFilterCriterion = "vid"
	VLANFilterCriterionPriority VLANFilterCriterion = "priority"
	VLANFilterCriterionTCI      VLANFilterCriterion = "tci"
)

type ExtendedVLAN struct {
	EntityID        uint16      `json:"entity_id"`
	AssociationType uint8       `json:"association_type"`
	AssociatedClass me.ClassID  `json:"associated_class"`
	AssociatedME    uint16      `json:"associated_me"`
	InputTPID       uint16      `json:"input_tpid"`
	OutputTPID      uint16      `json:"output_tpid"`
	DownstreamMode  uint8       `json:"downstream_mode"`
	EnhancedMode    uint8       `json:"enhanced_mode"`
	DSCPToPBit      [64]uint8   `json:"dscp_to_pbit"`
	Rules           []vlan.Rule `json:"rules"`
}

type VLANOperation struct {
	EntityID        uint16     `json:"entity_id"`
	AssociationType uint8      `json:"association_type"`
	AssociatedClass me.ClassID `json:"associated_class"`
	AssociatedME    uint16     `json:"associated_me"`
	UpstreamMode    uint8      `json:"upstream_mode"`
	UpstreamTCI     uint16     `json:"upstream_tci"`
	DownstreamMode  uint8      `json:"downstream_mode"`
}

// ValidateServiceGraph rejects mutations that would leave hardware-facing
// Ethernet service references ambiguous or dangling.
func ValidateServiceGraph(snapshot []mib.Instance) error {
	_, err := BuildServiceGraph(snapshot)
	return err
}

// BuildServiceGraph resolves the OMCI MIB into the connectivity needed by an
// Airoha backend. Null mapper branches remain present as 0xffff so the OLT can
// construct a service over several transactions.
func BuildServiceGraph(snapshot []mib.Instance) (ServiceGraph, error) {
	instances := make(map[mib.Key]mib.Instance, len(snapshot))
	for _, instance := range snapshot {
		if _, exists := instances[instance.Key]; exists {
			return ServiceGraph{}, fmt.Errorf("duplicate managed entity %d/%#x", instance.ClassID, instance.EntityID)
		}
		instances[instance.Key] = instance
	}

	graph := ServiceGraph{}
	tconts := make(map[uint16]TCONT)
	activeAllocIDs := make(map[uint16]uint16)
	unis := make(map[uint16]EthernetUNI)
	bridges := make(map[uint16]*MACBridge)
	mappers := make(map[uint16]PBitMapper)
	gemPorts := make(map[uint16]GEMPort)
	gemInterworking := make(map[uint16]GEMInterworking)

	trafficManagementOption := uint8(0)
	if onu, found := instances[mib.Key{ClassID: me.OnuGClassID, EntityID: 0}]; found {
		var err error
		trafficManagementOption, err = uint8AttributeDefault(onu, me.OnuG_TrafficManagementOption, 0)
		if err != nil {
			return ServiceGraph{}, err
		}
		if trafficManagementOption > 2 {
			return ServiceGraph{}, fmt.Errorf("ONU-G has invalid traffic management option %d", trafficManagementOption)
		}
	}

	for _, instance := range snapshot {
		switch instance.ClassID {
		case me.PhysicalPathTerminationPointEthernetUniClassID:
			interfaceName, err := ethernetUNIInterface(instance.EntityID)
			if err != nil {
				return ServiceGraph{}, err
			}
			administrative, err := uint8AttributeDefault(instance,
				me.PhysicalPathTerminationPointEthernetUni_AdministrativeState, 0)
			if err != nil {
				return ServiceGraph{}, err
			}
			operational, err := uint8AttributeDefault(instance,
				me.PhysicalPathTerminationPointEthernetUni_OperationalState, 1)
			if err != nil {
				return ServiceGraph{}, err
			}
			configuration, err := uint8AttributeDefault(instance,
				me.PhysicalPathTerminationPointEthernetUni_ConfigurationInd, 0)
			if err != nil {
				return ServiceGraph{}, err
			}
			uni := EthernetUNI{EntityID: instance.EntityID, Interface: interfaceName,
				AdministrativeState: administrative, OperationalState: operational,
				Configuration: configuration}
			unis[instance.EntityID] = uni
			graph.UNIs = append(graph.UNIs, uni)

		case me.TContClassID:
			allocID, err := uint16Attribute(instance, me.TCont_AllocId)
			if err != nil {
				return ServiceGraph{}, err
			}
			if allocID != nullPointer && allocID > 0x0fff {
				return ServiceGraph{}, fmt.Errorf("T-CONT %#x has out-of-range Alloc-ID %d", instance.EntityID, allocID)
			}
			if previous, duplicate := activeAllocIDs[allocID]; allocID != nullPointer && duplicate {
				return ServiceGraph{}, fmt.Errorf("Alloc-ID %d is shared by T-CONT %#x and %#x", allocID, previous, instance.EntityID)
			}
			if allocID != nullPointer {
				activeAllocIDs[allocID] = instance.EntityID
			}
			tcont := TCONT{EntityID: instance.EntityID, AllocID: allocID}
			tconts[instance.EntityID] = tcont
			graph.TCONTs = append(graph.TCONTs, tcont)

		case me.MacBridgeServiceProfileClassID:
			bridge, err := buildMACBridge(instance)
			if err != nil {
				return ServiceGraph{}, err
			}
			bridges[instance.EntityID] = bridge

		case me.Ieee8021PMapperServiceProfileClassID:
			mapper, err := buildMapper(instance)
			if err != nil {
				return ServiceGraph{}, err
			}
			mappers[instance.EntityID] = mapper
			graph.Mappers = append(graph.Mappers, mapper)
		}
	}

	gemPortIDs := make(map[uint16]uint16)
	for _, instance := range snapshot {
		if instance.ClassID != me.GemPortNetworkCtpClassID {
			continue
		}
		portID, err := uint16Attribute(instance, me.GemPortNetworkCtp_PortId)
		if err != nil {
			return ServiceGraph{}, err
		}
		if portID > 0x0fff {
			return ServiceGraph{}, fmt.Errorf("GEM CTP %#x has out-of-range port ID %d", instance.EntityID, portID)
		}
		if previous, duplicate := gemPortIDs[portID]; duplicate {
			return ServiceGraph{}, fmt.Errorf("GEM port ID %d cannot be mapped by XG2010G CTPs %#x and %#x", portID, previous, instance.EntityID)
		}
		gemPortIDs[portID] = instance.EntityID

		tcontID, err := uint16Attribute(instance, me.GemPortNetworkCtp_TContPointer)
		if err != nil {
			return ServiceGraph{}, err
		}
		tcont, exists := tconts[tcontID]
		if !exists {
			return ServiceGraph{}, fmt.Errorf("GEM CTP %#x references missing T-CONT %#x", instance.EntityID, tcontID)
		}
		direction, err := uint8Attribute(instance, me.GemPortNetworkCtp_Direction)
		if err != nil {
			return ServiceGraph{}, err
		}
		if direction < 1 || direction > 3 {
			return ServiceGraph{}, fmt.Errorf("GEM CTP %#x has invalid direction %d", instance.EntityID, direction)
		}
		upstreamQueue, err := uint16Attribute(instance, me.GemPortNetworkCtp_TrafficManagementPointerForUpstream)
		if err != nil {
			return ServiceGraph{}, err
		}
		if err := validateUpstreamTrafficPointer(instances, instance.EntityID, tcontID,
			upstreamQueue, trafficManagementOption); err != nil {
			return ServiceGraph{}, err
		}
		downstreamQueue, err := uint16Attribute(instance, me.GemPortNetworkCtp_PriorityQueuePointerForDownStream)
		if err != nil {
			return ServiceGraph{}, err
		}
		if downstreamQueue != nullPointer && !hasInstance(instances, me.PriorityQueueClassID, downstreamQueue) {
			return ServiceGraph{}, fmt.Errorf("GEM CTP %#x references missing downstream priority queue %#x", instance.EntityID, downstreamQueue)
		}
		encryptionRing, err := uint8AttributeDefault(instance, me.GemPortNetworkCtp_EncryptionKeyRing, 0)
		if err != nil {
			return ServiceGraph{}, err
		}
		if encryptionRing > 3 {
			return ServiceGraph{}, fmt.Errorf("GEM CTP %#x has invalid encryption key ring %d", instance.EntityID, encryptionRing)
		}
		gemPort := GEMPort{EntityID: instance.EntityID, PortID: portID, TCONT: tcontID,
			AllocID: tcont.AllocID, Direction: direction, UpstreamQueue: upstreamQueue,
			DownstreamQueue: downstreamQueue, EncryptionRing: encryptionRing}
		gemPorts[instance.EntityID] = gemPort
		graph.GEMPorts = append(graph.GEMPorts, gemPort)
	}

	for _, instance := range snapshot {
		if instance.ClassID != me.GemInterworkingTerminationPointClassID {
			continue
		}
		interworking, err := buildGEMInterworking(instance, instances, gemPorts, bridges, mappers)
		if err != nil {
			return ServiceGraph{}, err
		}
		gemInterworking[instance.EntityID] = interworking
		graph.Interworking = append(graph.Interworking, interworking)
	}

	for _, mapper := range graph.Mappers {
		if err := validateMapper(mapper, instances, gemInterworking); err != nil {
			return ServiceGraph{}, err
		}
	}

	bridgePortNumbers := make(map[uint16]map[uint8]uint16)
	bridgePorts := make(map[uint16]MACBridgePort)
	for _, instance := range snapshot {
		if instance.ClassID != me.MacBridgePortConfigurationDataClassID {
			continue
		}
		bridgeID, err := uint16Attribute(instance, me.MacBridgePortConfigurationData_BridgeIdPointer)
		if err != nil {
			return ServiceGraph{}, err
		}
		bridge, exists := bridges[bridgeID]
		if !exists {
			return ServiceGraph{}, fmt.Errorf("MAC bridge port %#x references missing bridge profile %#x", instance.EntityID, bridgeID)
		}
		portNumber, err := uint8Attribute(instance, me.MacBridgePortConfigurationData_PortNum)
		if err != nil {
			return ServiceGraph{}, err
		}
		if bridgePortNumbers[bridgeID] == nil {
			bridgePortNumbers[bridgeID] = make(map[uint8]uint16)
		}
		if previous, duplicate := bridgePortNumbers[bridgeID][portNumber]; duplicate {
			return ServiceGraph{}, fmt.Errorf("bridge %#x port number %d is shared by MEs %#x and %#x",
				bridgeID, portNumber, previous, instance.EntityID)
		}
		bridgePortNumbers[bridgeID][portNumber] = instance.EntityID
		tpType, err := uint8Attribute(instance, me.MacBridgePortConfigurationData_TpType)
		if err != nil {
			return ServiceGraph{}, err
		}
		tp, err := uint16Attribute(instance, me.MacBridgePortConfigurationData_TpPointer)
		if err != nil {
			return ServiceGraph{}, err
		}
		if err := validateBridgePortTP(instance.EntityID, bridgeID, tpType, tp, instances, gemInterworking); err != nil {
			return ServiceGraph{}, err
		}
		port, err := buildMACBridgePort(instance, portNumber, tpType, tp)
		if err != nil {
			return ServiceGraph{}, err
		}
		bridge.Ports = append(bridge.Ports, port)
		bridgePorts[instance.EntityID] = port
	}

	associations := make(map[mib.Key]uint16)
	for _, instance := range snapshot {
		switch instance.ClassID {
		case me.VlanTaggingFilterDataClassID:
			filter, err := buildVLANFilter(instance, bridgePorts)
			if err != nil {
				return ServiceGraph{}, err
			}
			graph.VLANFilters = append(graph.VLANFilters, filter)

		case me.VlanTaggingOperationConfigurationDataClassID:
			operation, err := buildVLANOperation(instance, instances)
			if err != nil {
				return ServiceGraph{}, err
			}
			association := mib.Key{ClassID: operation.AssociatedClass, EntityID: operation.AssociatedME}
			if previous, duplicate := associations[association]; duplicate {
				return ServiceGraph{}, fmt.Errorf("VLAN operation MEs %#x and %#x share target %d/%#x",
					previous, operation.EntityID, association.ClassID, association.EntityID)
			}
			associations[association] = operation.EntityID
			graph.VLANOperations = append(graph.VLANOperations, operation)

		case me.ExtendedVlanTaggingOperationConfigurationDataClassID:
			extended, err := buildExtendedVLAN(instance, instances)
			if err != nil {
				return ServiceGraph{}, err
			}
			association := mib.Key{ClassID: extended.AssociatedClass, EntityID: extended.AssociatedME}
			if previous, duplicate := associations[association]; duplicate {
				return ServiceGraph{}, fmt.Errorf("VLAN operation MEs %#x and %#x share target %d/%#x",
					previous, extended.EntityID, association.ClassID, association.EntityID)
			}
			associations[association] = extended.EntityID
			graph.ExtendedVLANs = append(graph.ExtendedVLANs, extended)
		}
	}

	for _, bridge := range bridges {
		sort.Slice(bridge.Ports, func(i, j int) bool { return bridge.Ports[i].Port < bridge.Ports[j].Port })
		graph.Bridges = append(graph.Bridges, *bridge)
	}
	sortServiceGraph(&graph)
	return graph, nil
}

func buildVLANOperation(instance mib.Instance, instances map[mib.Key]mib.Instance) (VLANOperation, error) {
	upstreamMode, err := uint8Attribute(instance,
		me.VlanTaggingOperationConfigurationData_UpstreamVlanTaggingOperationMode)
	if err != nil {
		return VLANOperation{}, err
	}
	if upstreamMode > 2 {
		return VLANOperation{}, fmt.Errorf("VLAN operation ME %#x has invalid upstream mode %d",
			instance.EntityID, upstreamMode)
	}
	tci, err := uint16Attribute(instance, me.VlanTaggingOperationConfigurationData_UpstreamVlanTagTciValue)
	if err != nil {
		return VLANOperation{}, err
	}
	if upstreamMode != 0 && tci&0x0fff == 0x0fff {
		return VLANOperation{}, fmt.Errorf("VLAN operation ME %#x uses reserved upstream VID 4095",
			instance.EntityID)
	}
	downstreamMode, err := uint8Attribute(instance,
		me.VlanTaggingOperationConfigurationData_DownstreamVlanTaggingOperationMode)
	if err != nil {
		return VLANOperation{}, err
	}
	if downstreamMode > 1 {
		return VLANOperation{}, fmt.Errorf("VLAN operation ME %#x has invalid downstream mode %d",
			instance.EntityID, downstreamMode)
	}
	associationType, err := uint8AttributeDefault(instance,
		me.VlanTaggingOperationConfigurationData_AssociationType, 0)
	if err != nil {
		return VLANOperation{}, err
	}
	pointer := instance.EntityID
	if associationType != 0 {
		pointer, err = uint16Attribute(instance,
			me.VlanTaggingOperationConfigurationData_AssociatedMePointer)
		if err != nil {
			return VLANOperation{}, err
		}
	}
	var targetClass me.ClassID
	switch associationType {
	case 0, 10:
		targetClass = me.PhysicalPathTerminationPointEthernetUniClassID
	case 2:
		targetClass = me.Ieee8021PMapperServiceProfileClassID
	case 3:
		targetClass = me.MacBridgePortConfigurationDataClassID
	case 5:
		targetClass = me.GemInterworkingTerminationPointClassID
	default:
		return VLANOperation{}, fmt.Errorf("VLAN operation ME %#x uses unsupported association type %d",
			instance.EntityID, associationType)
	}
	if !hasInstance(instances, targetClass, pointer) {
		return VLANOperation{}, fmt.Errorf("VLAN operation ME %#x association type %d references missing class %d/%#x",
			instance.EntityID, associationType, targetClass, pointer)
	}
	return VLANOperation{EntityID: instance.EntityID, AssociationType: associationType,
		AssociatedClass: targetClass, AssociatedME: pointer, UpstreamMode: upstreamMode,
		UpstreamTCI: tci, DownstreamMode: downstreamMode}, nil
}

func buildMACBridge(instance mib.Instance) (*MACBridge, error) {
	bridge := &MACBridge{EntityID: instance.EntityID}
	var err error
	bridge.SpanningTree, err = booleanAttributeDefault(instance,
		me.MacBridgeServiceProfile_SpanningTreeInd, 0)
	if err != nil {
		return nil, err
	}
	bridge.Learning, err = booleanAttributeDefault(instance,
		me.MacBridgeServiceProfile_LearningInd, 0)
	if err != nil {
		return nil, err
	}
	bridge.PortBridging, err = booleanAttributeDefault(instance,
		me.MacBridgeServiceProfile_PortBridgingInd, 0)
	if err != nil {
		return nil, err
	}
	bridge.Priority, err = uint16AttributeDefault(instance, me.MacBridgeServiceProfile_Priority, 0)
	if err != nil {
		return nil, err
	}
	bridge.MaxAge, err = uint16AttributeDefault(instance, me.MacBridgeServiceProfile_MaxAge, 0x0600)
	if err != nil {
		return nil, err
	}
	if bridge.MaxAge < 0x0600 || bridge.MaxAge > 0x2800 {
		return nil, fmt.Errorf("MAC bridge %#x max age %#x is outside 6..40 seconds",
			instance.EntityID, bridge.MaxAge)
	}
	bridge.HelloTime, err = uint16AttributeDefault(instance, me.MacBridgeServiceProfile_HelloTime, 0x0100)
	if err != nil {
		return nil, err
	}
	if bridge.HelloTime < 0x0100 || bridge.HelloTime > 0x0a00 {
		return nil, fmt.Errorf("MAC bridge %#x hello time %#x is outside 1..10 seconds",
			instance.EntityID, bridge.HelloTime)
	}
	bridge.ForwardDelay, err = uint16AttributeDefault(instance,
		me.MacBridgeServiceProfile_ForwardDelay, 0x0400)
	if err != nil {
		return nil, err
	}
	if bridge.ForwardDelay < 0x0400 || bridge.ForwardDelay > 0x1e00 {
		return nil, fmt.Errorf("MAC bridge %#x forward delay %#x is outside 4..30 seconds",
			instance.EntityID, bridge.ForwardDelay)
	}
	bridge.UnknownMACDiscard, err = booleanAttributeDefault(instance,
		me.MacBridgeServiceProfile_UnknownMacAddressDiscard, 0)
	if err != nil {
		return nil, err
	}
	bridge.MACLearningDepth, err = uint8AttributeDefault(instance,
		me.MacBridgeServiceProfile_MacLearningDepth, 0)
	if err != nil {
		return nil, err
	}
	bridge.DynamicFilteringAgeTime, err = uint32AttributeDefault(instance,
		me.MacBridgeServiceProfile_DynamicFilteringAgeingTime, 300)
	if err != nil {
		return nil, err
	}
	if bridge.DynamicFilteringAgeTime != 0 &&
		(bridge.DynamicFilteringAgeTime < 10 || bridge.DynamicFilteringAgeTime > 1000000) {
		return nil, fmt.Errorf("MAC bridge %#x dynamic filtering age %d is outside 10..1000000 seconds",
			instance.EntityID, bridge.DynamicFilteringAgeTime)
	}
	return bridge, nil
}

func buildMACBridgePort(instance mib.Instance, portNumber, tpType uint8, tp uint16) (MACBridgePort, error) {
	port := MACBridgePort{EntityID: instance.EntityID, Port: portNumber, TPType: tpType, TP: tp}
	var err error
	port.Priority, err = uint16AttributeDefault(instance, me.MacBridgePortConfigurationData_PortPriority, 0)
	if err != nil {
		return MACBridgePort{}, err
	}
	port.PathCost, err = uint16AttributeDefault(instance, me.MacBridgePortConfigurationData_PortPathCost, 1)
	if err != nil {
		return MACBridgePort{}, err
	}
	if port.PathCost == 0 {
		return MACBridgePort{}, fmt.Errorf("MAC bridge port %#x has zero path cost", instance.EntityID)
	}
	port.SpanningTree, err = booleanAttributeDefault(instance,
		me.MacBridgePortConfigurationData_PortSpanningTreeInd, 0)
	if err != nil {
		return MACBridgePort{}, err
	}
	port.OutboundTD, err = uint16AttributeDefault(instance,
		me.MacBridgePortConfigurationData_OutboundTdPointer, nullPointer)
	if err != nil {
		return MACBridgePort{}, err
	}
	port.InboundTD, err = uint16AttributeDefault(instance,
		me.MacBridgePortConfigurationData_InboundTdPointer, nullPointer)
	if err != nil {
		return MACBridgePort{}, err
	}
	port.MACLearningDepth, err = uint8AttributeDefault(instance,
		me.MacBridgePortConfigurationData_MacLearningDepth, 0)
	if err != nil {
		return MACBridgePort{}, err
	}
	return port, nil
}

func booleanAttributeDefault(instance mib.Instance, name string, fallback uint8) (uint8, error) {
	value, err := uint8AttributeDefault(instance, name, fallback)
	if err != nil {
		return 0, err
	}
	if value > 1 {
		return 0, fmt.Errorf("managed entity %d/%#x attribute %s is not boolean: %d",
			instance.ClassID, instance.EntityID, name, value)
	}
	return value, nil
}

func ethernetUNIInterface(entityID uint16) (string, error) {
	switch entityID {
	case 0x0101:
		return "lan1", nil
	case 0x0102:
		return "lan2", nil
	case 0x0103:
		return "lan3", nil
	case 0x0104:
		return "lan4", nil
	default:
		return "", fmt.Errorf("Ethernet UNI %#x has no XG2010G interface mapping", entityID)
	}
}

func buildMapper(instance mib.Instance) (PBitMapper, error) {
	mapper := PBitMapper{EntityID: instance.EntityID}
	var err error
	mapper.TPPointer, err = uint16Attribute(instance, me.Ieee8021PMapperServiceProfile_TpPointer)
	if err != nil {
		return PBitMapper{}, err
	}
	mapper.TPType, err = uint8AttributeDefault(instance, me.Ieee8021PMapperServiceProfile_TpType, 0)
	if err != nil {
		return PBitMapper{}, err
	}
	for priority, name := range mapperPBitAttributes {
		mapper.PBits[priority], err = uint16Attribute(instance, name)
		if err != nil {
			return PBitMapper{}, err
		}
	}
	mapper.UnmarkedFrameOption, err = uint8AttributeDefault(instance,
		me.Ieee8021PMapperServiceProfile_UnmarkedFrameOption, 0)
	if err != nil {
		return PBitMapper{}, err
	}
	if mapper.UnmarkedFrameOption > 1 {
		return PBitMapper{}, fmt.Errorf("802.1p mapper %#x has invalid unmarked frame option %d",
			instance.EntityID, mapper.UnmarkedFrameOption)
	}
	mapper.DefaultPBit, err = uint8AttributeDefault(instance,
		me.Ieee8021PMapperServiceProfile_DefaultPBitAssumption, 0)
	if err != nil {
		return PBitMapper{}, err
	}
	if mapper.DefaultPBit > 7 {
		return PBitMapper{}, fmt.Errorf("802.1p mapper %#x has invalid default P-bit %d",
			instance.EntityID, mapper.DefaultPBit)
	}
	dscpBytes := make([]byte, 24)
	if _, present := instance.Attributes[me.Ieee8021PMapperServiceProfile_DscpToPBitMapping]; present {
		dscpBytes, err = bytesAttribute(instance, me.Ieee8021PMapperServiceProfile_DscpToPBitMapping, 24)
		if err != nil {
			return PBitMapper{}, err
		}
	}
	mapper.DSCPToPBit, err = vlan.DecodeDSCPMapping(dscpBytes)
	if err != nil {
		return PBitMapper{}, err
	}
	return mapper, nil
}

func validateMapper(mapper PBitMapper, instances map[mib.Key]mib.Instance,
	interworking map[uint16]GEMInterworking) error {
	switch mapper.TPType {
	case 0:
		if mapper.TPPointer != nullPointer {
			return fmt.Errorf("802.1p mapper %#x uses bridge mapping but TP pointer is %#x, want 0xffff",
				mapper.EntityID, mapper.TPPointer)
		}
	case 1:
		if !hasInstance(instances, me.PhysicalPathTerminationPointEthernetUniClassID, mapper.TPPointer) {
			return fmt.Errorf("802.1p mapper %#x references missing Ethernet UNI %#x", mapper.EntityID, mapper.TPPointer)
		}
	default:
		return fmt.Errorf("802.1p mapper %#x uses unsupported TP type %d", mapper.EntityID, mapper.TPType)
	}
	for priority, pointer := range mapper.PBits {
		if pointer == nullPointer {
			continue
		}
		iw, exists := interworking[pointer]
		if !exists {
			return fmt.Errorf("802.1p mapper %#x P-bit %d references missing GEM IW TP %#x",
				mapper.EntityID, priority, pointer)
		}
		if iw.Option != 5 || iw.ServiceProfile != mapper.EntityID {
			return fmt.Errorf("802.1p mapper %#x P-bit %d references GEM IW TP %#x owned by option %d/profile %#x",
				mapper.EntityID, priority, pointer, iw.Option, iw.ServiceProfile)
		}
		if mapper.TPType == 0 && iw.InterworkingTermination != 0 && iw.InterworkingTermination != nullPointer {
			return fmt.Errorf("bridging 802.1p mapper %#x P-bit %d GEM IW TP %#x has non-null interworking TP %#x",
				mapper.EntityID, priority, pointer, iw.InterworkingTermination)
		}
		if mapper.TPType == 1 && iw.InterworkingTermination != mapper.TPPointer {
			return fmt.Errorf("direct 802.1p mapper %#x P-bit %d GEM IW TP %#x points to UNI %#x, want %#x",
				mapper.EntityID, priority, pointer, iw.InterworkingTermination, mapper.TPPointer)
		}
	}
	return nil
}

func buildGEMInterworking(instance mib.Instance, instances map[mib.Key]mib.Instance,
	gemPorts map[uint16]GEMPort, bridges map[uint16]*MACBridge,
	mappers map[uint16]PBitMapper) (GEMInterworking, error) {
	pointer, err := uint16Attribute(instance,
		me.GemInterworkingTerminationPoint_GemPortNetworkCtpConnectivityPointer)
	if err != nil {
		return GEMInterworking{}, err
	}
	if _, exists := gemPorts[pointer]; !exists {
		return GEMInterworking{}, fmt.Errorf("GEM IW TP %#x references missing GEM CTP %#x", instance.EntityID, pointer)
	}
	option, err := uint8Attribute(instance, me.GemInterworkingTerminationPoint_InterworkingOption)
	if err != nil {
		return GEMInterworking{}, err
	}
	service, err := uint16Attribute(instance, me.GemInterworkingTerminationPoint_ServiceProfilePointer)
	if err != nil {
		return GEMInterworking{}, err
	}
	switch option {
	case 1:
		if _, exists := bridges[service]; !exists {
			return GEMInterworking{}, fmt.Errorf("GEM IW TP %#x option 1 references missing bridge profile %#x",
				instance.EntityID, service)
		}
	case 5:
		if _, exists := mappers[service]; !exists {
			return GEMInterworking{}, fmt.Errorf("GEM IW TP %#x option 5 references missing 802.1p mapper %#x",
				instance.EntityID, service)
		}
	default:
		return GEMInterworking{}, fmt.Errorf("GEM IW TP %#x uses unsupported interworking option %d", instance.EntityID, option)
	}
	interworkingTP, err := uint16Attribute(instance,
		me.GemInterworkingTerminationPoint_InterworkingTerminationPointPointer)
	if err != nil {
		return GEMInterworking{}, err
	}
	if option == 5 && interworkingTP != 0 && interworkingTP != nullPointer &&
		!hasInstance(instances, me.PhysicalPathTerminationPointEthernetUniClassID, interworkingTP) {
		return GEMInterworking{}, fmt.Errorf("GEM IW TP %#x references missing interworking Ethernet UNI %#x",
			instance.EntityID, interworkingTP)
	}
	gal, err := uint16Attribute(instance, me.GemInterworkingTerminationPoint_GalProfilePointer)
	if err != nil {
		return GEMInterworking{}, err
	}
	if !hasInstance(instances, me.GalEthernetProfileClassID, gal) {
		return GEMInterworking{}, fmt.Errorf("GEM IW TP %#x references missing GAL Ethernet profile %#x", instance.EntityID, gal)
	}
	return GEMInterworking{EntityID: instance.EntityID, GEMPort: pointer, Option: option,
		ServiceProfile: service, InterworkingTermination: interworkingTP, GALProfile: gal}, nil
}

func validateBridgePortTP(entityID, bridgeID uint16, tpType uint8, pointer uint16,
	instances map[mib.Key]mib.Instance, interworking map[uint16]GEMInterworking) error {
	var classID me.ClassID
	switch tpType {
	case 1:
		classID = me.PhysicalPathTerminationPointEthernetUniClassID
	case 3:
		classID = me.Ieee8021PMapperServiceProfileClassID
	case 5:
		classID = me.GemInterworkingTerminationPointClassID
	default:
		return fmt.Errorf("MAC bridge port %#x uses unsupported TP type %d", entityID, tpType)
	}
	if !hasInstance(instances, classID, pointer) {
		return fmt.Errorf("MAC bridge port %#x TP type %d references missing class %d/%#x",
			entityID, tpType, classID, pointer)
	}
	if tpType == 5 {
		iw := interworking[pointer]
		if iw.Option != 1 || iw.ServiceProfile != bridgeID {
			return fmt.Errorf("MAC bridge port %#x references GEM IW TP %#x option/profile %d/%#x, want 1/%#x",
				entityID, pointer, iw.Option, iw.ServiceProfile, bridgeID)
		}
	} else if tpType == 3 {
		mapper := instances[mib.Key{ClassID: me.Ieee8021PMapperServiceProfileClassID, EntityID: pointer}]
		mapperTPType, err := uint8AttributeDefault(mapper, me.Ieee8021PMapperServiceProfile_TpType, 0)
		if err != nil {
			return err
		}
		if mapperTPType != 0 {
			return fmt.Errorf("MAC bridge port %#x references non-bridging 802.1p mapper %#x TP type %d",
				entityID, pointer, mapperTPType)
		}
	}
	return nil
}

func buildVLANFilter(instance mib.Instance, bridgePorts map[uint16]MACBridgePort) (VLANFilter, error) {
	if _, exists := bridgePorts[instance.EntityID]; !exists {
		return VLANFilter{}, fmt.Errorf("VLAN filter %#x has no implicitly linked MAC bridge port", instance.EntityID)
	}
	list, err := bytesAttribute(instance, me.VlanTaggingFilterData_VlanFilterList, 24)
	if err != nil {
		return VLANFilter{}, err
	}
	count, err := uint8Attribute(instance, me.VlanTaggingFilterData_NumberOfEntries)
	if err != nil {
		return VLANFilter{}, err
	}
	if count > 12 {
		return VLANFilter{}, fmt.Errorf("VLAN filter %#x has %d entries, maximum is 12", instance.EntityID, count)
	}
	entries := make([]uint16, count)
	for index := range entries {
		entries[index] = uint16(list[index*2])<<8 | uint16(list[index*2+1])
		if entries[index]&0x0fff == 0x0fff {
			return VLANFilter{}, fmt.Errorf("VLAN filter %#x entry %d uses reserved VID 4095", instance.EntityID, index)
		}
	}
	operation, err := uint8Attribute(instance, me.VlanTaggingFilterData_ForwardOperation)
	if err != nil {
		return VLANFilter{}, err
	}
	taggedAction, criterion, untaggedAction, err := decodeVLANForwardOperation(operation)
	if err != nil {
		return VLANFilter{}, fmt.Errorf("VLAN filter %#x: %w", instance.EntityID, err)
	}
	return VLANFilter{EntityID: instance.EntityID, BridgePort: instance.EntityID,
		ForwardOperation: operation, TaggedAction: taggedAction, TaggedCriterion: criterion,
		UntaggedAction: untaggedAction, Entries: entries}, nil
}

func decodeVLANForwardOperation(operation uint8) (VLANFilterAction, VLANFilterCriterion,
	VLANFilterAction, error) {
	untagged := VLANFilterActionBridge
	if operation == 0x02 || operation == 0x04 || operation == 0x06 || operation == 0x08 ||
		operation == 0x0a || operation == 0x0c || operation == 0x0e || operation == 0x10 ||
		operation == 0x12 || operation == 0x14 || operation == 0x15 || operation == 0x17 ||
		operation == 0x19 || operation == 0x1b || operation == 0x1d || operation == 0x1f ||
		operation == 0x21 {
		untagged = VLANFilterActionDiscard
	}

	switch operation {
	case 0x00, 0x02, 0x15:
		return VLANFilterActionBridge, VLANFilterCriterionNone, untagged, nil
	case 0x01:
		return VLANFilterActionDiscard, VLANFilterCriterionNone, untagged, nil
	case 0x03, 0x04, 0x0f, 0x10, 0x1c, 0x1d:
		return VLANFilterActionPositive, VLANFilterCriterionVID, untagged, nil
	case 0x05, 0x06:
		return VLANFilterActionNegative, VLANFilterCriterionVID, untagged, nil
	case 0x07, 0x08, 0x11, 0x12, 0x1e, 0x1f:
		return VLANFilterActionPositive, VLANFilterCriterionPriority, untagged, nil
	case 0x09, 0x0a:
		return VLANFilterActionNegative, VLANFilterCriterionPriority, untagged, nil
	case 0x0b, 0x0c, 0x13, 0x14, 0x20, 0x21:
		return VLANFilterActionPositive, VLANFilterCriterionTCI, untagged, nil
	case 0x0d, 0x0e:
		return VLANFilterActionNegative, VLANFilterCriterionTCI, untagged, nil
	case 0x16, 0x17:
		return VLANFilterActionPositiveDA, VLANFilterCriterionVID, untagged, nil
	case 0x18, 0x19:
		return VLANFilterActionPositiveDA, VLANFilterCriterionPriority, untagged, nil
	case 0x1a, 0x1b:
		return VLANFilterActionPositiveDA, VLANFilterCriterionTCI, untagged, nil
	default:
		return "", "", "", fmt.Errorf("forward operation %#02x is reserved", operation)
	}
}

func buildExtendedVLAN(instance mib.Instance, instances map[mib.Key]mib.Instance) (ExtendedVLAN, error) {
	associationType, err := uint8Attribute(instance,
		me.ExtendedVlanTaggingOperationConfigurationData_AssociationType)
	if err != nil {
		return ExtendedVLAN{}, err
	}
	pointer, err := uint16Attribute(instance,
		me.ExtendedVlanTaggingOperationConfigurationData_AssociatedMePointer)
	if err != nil {
		return ExtendedVLAN{}, err
	}
	var targetClass me.ClassID
	switch associationType {
	case 0:
		targetClass = me.MacBridgePortConfigurationDataClassID
	case 1:
		targetClass = me.Ieee8021PMapperServiceProfileClassID
	case 2:
		targetClass = me.PhysicalPathTerminationPointEthernetUniClassID
	case 5:
		targetClass = me.GemInterworkingTerminationPointClassID
	default:
		return ExtendedVLAN{}, fmt.Errorf("extended VLAN ME %#x uses unsupported association type %d",
			instance.EntityID, associationType)
	}
	if !hasInstance(instances, targetClass, pointer) {
		return ExtendedVLAN{}, fmt.Errorf("extended VLAN ME %#x association type %d references missing class %d/%#x",
			instance.EntityID, associationType, targetClass, pointer)
	}
	inputTPID, err := uint16Attribute(instance, me.ExtendedVlanTaggingOperationConfigurationData_InputTpid)
	if err != nil {
		return ExtendedVLAN{}, err
	}
	outputTPID, err := uint16Attribute(instance, me.ExtendedVlanTaggingOperationConfigurationData_OutputTpid)
	if err != nil {
		return ExtendedVLAN{}, err
	}
	downstream, err := uint8Attribute(instance, me.ExtendedVlanTaggingOperationConfigurationData_DownstreamMode)
	if err != nil {
		return ExtendedVLAN{}, err
	}
	if downstream > 8 {
		return ExtendedVLAN{}, fmt.Errorf("extended VLAN ME %#x has invalid downstream mode %d", instance.EntityID, downstream)
	}
	enhanced, err := uint8AttributeDefault(instance, me.ExtendedVlanTaggingOperationConfigurationData_EnhancedMode, 0)
	if err != nil {
		return ExtendedVLAN{}, err
	}
	if enhanced > 1 {
		return ExtendedVLAN{}, fmt.Errorf("extended VLAN ME %#x has invalid enhanced mode %d", instance.EntityID, enhanced)
	}
	name := me.ExtendedVlanTaggingOperationConfigurationData_ReceivedFrameVlanTaggingOperationTable
	rowSize := vlan.ClassicRowSize
	if enhanced == 1 {
		name = me.ExtendedVlanTaggingOperationConfigurationData_EnhancedReceivedFrameClassificationAndProcessingTable
		rowSize = vlan.EnhancedRowSize
	}
	rules, err := tableAttributeDefault(instance, name, rowSize)
	if err != nil {
		return ExtendedVLAN{}, err
	}
	maximum, err := uint16AttributeDefault(instance,
		me.ExtendedVlanTaggingOperationConfigurationData_ReceivedFrameVlanTaggingOperationTableMaxSize, 0)
	if err != nil {
		return ExtendedVLAN{}, err
	}
	if maximum != 0 && rules.NumRows > int(maximum) {
		return ExtendedVLAN{}, fmt.Errorf("extended VLAN ME %#x has %d rules, maximum is %d",
			instance.EntityID, rules.NumRows, maximum)
	}
	parsedRules, err := vlan.ParseRows(rules.Rows, enhanced == 1)
	if err != nil {
		return ExtendedVLAN{}, fmt.Errorf("extended VLAN ME %#x: %w", instance.EntityID, err)
	}
	dscpBytes := make([]byte, 24)
	if _, present := instance.Attributes[me.ExtendedVlanTaggingOperationConfigurationData_DscpToPBitMapping]; present {
		dscpBytes, err = bytesAttribute(instance,
			me.ExtendedVlanTaggingOperationConfigurationData_DscpToPBitMapping, 24)
		if err != nil {
			return ExtendedVLAN{}, err
		}
	}
	dscpMapping, err := vlan.DecodeDSCPMapping(dscpBytes)
	if err != nil {
		return ExtendedVLAN{}, err
	}
	return ExtendedVLAN{EntityID: instance.EntityID, AssociationType: associationType,
		AssociatedClass: targetClass, AssociatedME: pointer, InputTPID: inputTPID, OutputTPID: outputTPID,
		DownstreamMode: downstream, EnhancedMode: enhanced, DSCPToPBit: dscpMapping,
		Rules: parsedRules}, nil
}

func validateUpstreamTrafficPointer(instances map[mib.Key]mib.Instance, gem, tcont,
	pointer uint16, option uint8) error {
	if option == 1 {
		if pointer != tcont {
			return fmt.Errorf("GEM CTP %#x rate-controlled upstream pointer %#x does not match T-CONT %#x",
				gem, pointer, tcont)
		}
		return nil
	}
	queue, exists := instances[mib.Key{ClassID: me.PriorityQueueClassID, EntityID: pointer}]
	if !exists {
		return fmt.Errorf("GEM CTP %#x references missing upstream priority queue %#x", gem, pointer)
	}
	related, err := uint32Attribute(queue, me.PriorityQueue_RelatedPort)
	if err != nil {
		return err
	}
	if uint16(related>>16) != tcont {
		return fmt.Errorf("GEM CTP %#x upstream queue %#x belongs to %#x, not T-CONT %#x",
			gem, pointer, uint16(related>>16), tcont)
	}
	return nil
}

func sortServiceGraph(graph *ServiceGraph) {
	sort.Slice(graph.UNIs, func(i, j int) bool { return graph.UNIs[i].EntityID < graph.UNIs[j].EntityID })
	sort.Slice(graph.TCONTs, func(i, j int) bool { return graph.TCONTs[i].EntityID < graph.TCONTs[j].EntityID })
	sort.Slice(graph.GEMPorts, func(i, j int) bool { return graph.GEMPorts[i].EntityID < graph.GEMPorts[j].EntityID })
	sort.Slice(graph.Interworking, func(i, j int) bool { return graph.Interworking[i].EntityID < graph.Interworking[j].EntityID })
	sort.Slice(graph.Mappers, func(i, j int) bool { return graph.Mappers[i].EntityID < graph.Mappers[j].EntityID })
	sort.Slice(graph.Bridges, func(i, j int) bool { return graph.Bridges[i].EntityID < graph.Bridges[j].EntityID })
	sort.Slice(graph.VLANFilters, func(i, j int) bool { return graph.VLANFilters[i].EntityID < graph.VLANFilters[j].EntityID })
	sort.Slice(graph.VLANOperations, func(i, j int) bool { return graph.VLANOperations[i].EntityID < graph.VLANOperations[j].EntityID })
	sort.Slice(graph.ExtendedVLANs, func(i, j int) bool { return graph.ExtendedVLANs[i].EntityID < graph.ExtendedVLANs[j].EntityID })
}

func hasInstance(instances map[mib.Key]mib.Instance, classID me.ClassID, entityID uint16) bool {
	_, exists := instances[mib.Key{ClassID: classID, EntityID: entityID}]
	return exists
}

func uint16Attribute(instance mib.Instance, name string) (uint16, error) {
	value, present := instance.Attributes[name]
	if !present {
		return 0, fmt.Errorf("managed entity %d/%#x is missing %s", instance.ClassID, instance.EntityID, name)
	}
	typed, ok := value.(uint16)
	if !ok {
		return 0, fmt.Errorf("managed entity %d/%#x attribute %s has type %T, want uint16",
			instance.ClassID, instance.EntityID, name, value)
	}
	return typed, nil
}

func uint16AttributeDefault(instance mib.Instance, name string, fallback uint16) (uint16, error) {
	if _, present := instance.Attributes[name]; !present {
		return fallback, nil
	}
	return uint16Attribute(instance, name)
}

func uint8Attribute(instance mib.Instance, name string) (uint8, error) {
	value, present := instance.Attributes[name]
	if !present {
		return 0, fmt.Errorf("managed entity %d/%#x is missing %s", instance.ClassID, instance.EntityID, name)
	}
	typed, ok := value.(uint8)
	if !ok {
		return 0, fmt.Errorf("managed entity %d/%#x attribute %s has type %T, want uint8",
			instance.ClassID, instance.EntityID, name, value)
	}
	return typed, nil
}

func uint8AttributeDefault(instance mib.Instance, name string, fallback uint8) (uint8, error) {
	if _, present := instance.Attributes[name]; !present {
		return fallback, nil
	}
	return uint8Attribute(instance, name)
}

func uint32Attribute(instance mib.Instance, name string) (uint32, error) {
	value, present := instance.Attributes[name]
	if !present {
		return 0, fmt.Errorf("managed entity %d/%#x is missing %s", instance.ClassID, instance.EntityID, name)
	}
	typed, ok := value.(uint32)
	if !ok {
		return 0, fmt.Errorf("managed entity %d/%#x attribute %s has type %T, want uint32",
			instance.ClassID, instance.EntityID, name, value)
	}
	return typed, nil
}

func uint32AttributeDefault(instance mib.Instance, name string, fallback uint32) (uint32, error) {
	if _, present := instance.Attributes[name]; !present {
		return fallback, nil
	}
	return uint32Attribute(instance, name)
}

func bytesAttribute(instance mib.Instance, name string, size int) ([]byte, error) {
	value, present := instance.Attributes[name]
	if !present {
		return nil, fmt.Errorf("managed entity %d/%#x is missing %s", instance.ClassID, instance.EntityID, name)
	}
	typed, ok := value.([]byte)
	if !ok || len(typed) != size {
		return nil, fmt.Errorf("managed entity %d/%#x attribute %s has type/length %T/%d, want []byte/%d",
			instance.ClassID, instance.EntityID, name, value, len(typed), size)
	}
	return append([]byte(nil), typed...), nil
}

func tableAttributeDefault(instance mib.Instance, name string, rowSize int) (me.TableRows, error) {
	value, present := instance.Attributes[name]
	if !present || value == nil {
		return me.TableRows{}, nil
	}
	typed, ok := value.(me.TableRows)
	if !ok || typed.NumRows < 0 || len(typed.Rows) != typed.NumRows*rowSize {
		return me.TableRows{}, fmt.Errorf("managed entity %d/%#x attribute %s has invalid table rows",
			instance.ClassID, instance.EntityID, name)
	}
	return me.TableRows{NumRows: typed.NumRows, Rows: append([]byte(nil), typed.Rows...)}, nil
}
