// SPDX-License-Identifier: Apache-2.0

package platform

import (
	"fmt"
	"sort"

	me "github.com/opencord/omci-lib-go/v2/generated"
	"github.com/xg2010g/airoha-omci/internal/mib"
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
	UNIs          []EthernetUNI     `json:"unis"`
	TCONTs        []TCONT           `json:"tconts"`
	GEMPorts      []GEMPort         `json:"gem_ports"`
	Interworking  []GEMInterworking `json:"gem_interworking"`
	Mappers       []PBitMapper      `json:"pbit_mappers"`
	Bridges       []MACBridge       `json:"bridges"`
	VLANFilters   []VLANFilter      `json:"vlan_filters"`
	ExtendedVLANs []ExtendedVLAN    `json:"extended_vlans"`
}

type EthernetUNI struct {
	EntityID            uint16 `json:"entity_id"`
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
	EntityID  uint16    `json:"entity_id"`
	TPType    uint8     `json:"tp_type"`
	TPPointer uint16    `json:"tp_pointer"`
	PBits     [8]uint16 `json:"pbits"`
}

type MACBridge struct {
	EntityID uint16          `json:"entity_id"`
	Ports    []MACBridgePort `json:"ports"`
}

type MACBridgePort struct {
	EntityID uint16 `json:"entity_id"`
	Port     uint8  `json:"port"`
	TPType   uint8  `json:"tp_type"`
	TP       uint16 `json:"tp"`
}

type VLANFilter struct {
	EntityID         uint16   `json:"entity_id"`
	BridgePort       uint16   `json:"bridge_port"`
	ForwardOperation uint8    `json:"forward_operation"`
	Entries          []uint16 `json:"entries"`
}

type ExtendedVLAN struct {
	EntityID        uint16       `json:"entity_id"`
	AssociationType uint8        `json:"association_type"`
	AssociatedME    uint16       `json:"associated_me"`
	InputTPID       uint16       `json:"input_tpid"`
	OutputTPID      uint16       `json:"output_tpid"`
	DownstreamMode  uint8        `json:"downstream_mode"`
	EnhancedMode    uint8        `json:"enhanced_mode"`
	Rules           me.TableRows `json:"rules"`
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
			uni := EthernetUNI{EntityID: instance.EntityID, AdministrativeState: administrative,
				OperationalState: operational, Configuration: configuration}
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
			bridge := &MACBridge{EntityID: instance.EntityID}
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
		port := MACBridgePort{EntityID: instance.EntityID, Port: portNumber, TPType: tpType, TP: tp}
		bridge.Ports = append(bridge.Ports, port)
		bridgePorts[instance.EntityID] = port
	}

	associations := make(map[[2]uint16]uint16)
	for _, instance := range snapshot {
		switch instance.ClassID {
		case me.VlanTaggingFilterDataClassID:
			filter, err := buildVLANFilter(instance, bridgePorts)
			if err != nil {
				return ServiceGraph{}, err
			}
			graph.VLANFilters = append(graph.VLANFilters, filter)

		case me.ExtendedVlanTaggingOperationConfigurationDataClassID:
			extended, err := buildExtendedVLAN(instance, instances)
			if err != nil {
				return ServiceGraph{}, err
			}
			association := [2]uint16{uint16(extended.AssociationType), extended.AssociatedME}
			if previous, duplicate := associations[association]; duplicate {
				return ServiceGraph{}, fmt.Errorf("extended VLAN MEs %#x and %#x share association type %d target %#x",
					previous, extended.EntityID, extended.AssociationType, extended.AssociatedME)
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
	return VLANFilter{EntityID: instance.EntityID, BridgePort: instance.EntityID,
		ForwardOperation: operation, Entries: entries}, nil
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
	rowSize := 16
	if enhanced == 1 {
		name = me.ExtendedVlanTaggingOperationConfigurationData_EnhancedReceivedFrameClassificationAndProcessingTable
		rowSize = 28
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
	return ExtendedVLAN{EntityID: instance.EntityID, AssociationType: associationType,
		AssociatedME: pointer, InputTPID: inputTPID, OutputTPID: outputTPID,
		DownstreamMode: downstream, EnhancedMode: enhanced, Rules: rules}, nil
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
