// SPDX-License-Identifier: Apache-2.0

package platform

import (
	"reflect"
	"strings"
	"testing"

	me "github.com/opencord/omci-lib-go/v2/generated"
	"github.com/xg2010g/airoha-omci/internal/mib"
	"github.com/xg2010g/airoha-omci/internal/model"
)

const (
	testUNI       = 0x0101
	testTCONT     = 0x8001
	testBridge    = 0x0100
	testMapper    = 0x0200
	testGAL       = 0x0001
	testMapperIW  = 0x0300
	testBridgeIW  = 0x0301
	testMapperGEM = 0x0400
	testBridgeGEM = 0x0401
	uniBridgePort = 0x0500
	mapperPort    = 0x0501
	gemBridgePort = 0x0502
)

func TestBuildServiceGraphResolvesCompleteEthernetService(t *testing.T) {
	snapshot := validServiceSnapshot()
	graph, err := BuildServiceGraph(snapshot)
	if err != nil {
		t.Fatalf("BuildServiceGraph() error = %v", err)
	}
	if len(graph.UNIs) != 1 || graph.UNIs[0].EntityID != testUNI ||
		len(graph.TCONTs) != 1 || graph.TCONTs[0].AllocID != 100 ||
		len(graph.GEMPorts) != 2 || len(graph.Interworking) != 2 ||
		len(graph.Mappers) != 1 || len(graph.Bridges) != 1 ||
		len(graph.Bridges[0].Ports) != 3 || len(graph.VLANFilters) != 1 ||
		len(graph.ExtendedVLANs) != 1 {
		t.Fatalf("resolved graph = %#v", graph)
	}
	for priority, pointer := range graph.Mappers[0].PBits {
		if pointer != testMapperIW {
			t.Fatalf("P-bit %d pointer = %#x, want %#x", priority, pointer, testMapperIW)
		}
	}
	if graph.GEMPorts[0].AllocID != 100 || graph.GEMPorts[1].AllocID != 100 {
		t.Fatalf("GEM Alloc-IDs = %d/%d, want 100", graph.GEMPorts[0].AllocID, graph.GEMPorts[1].AllocID)
	}
	if got := graph.ExtendedVLANs[0].Rules; got.NumRows != 1 || len(got.Rows) != 16 {
		t.Fatalf("extended VLAN rules = %#v, want one row", got)
	}
}

func TestBuildServiceGraphIsDeterministic(t *testing.T) {
	snapshot := validServiceSnapshot()
	forward, err := BuildServiceGraph(snapshot)
	if err != nil {
		t.Fatalf("BuildServiceGraph(forward) error = %v", err)
	}
	for left, right := 0, len(snapshot)-1; left < right; left, right = left+1, right-1 {
		snapshot[left], snapshot[right] = snapshot[right], snapshot[left]
	}
	reversed, err := BuildServiceGraph(snapshot)
	if err != nil {
		t.Fatalf("BuildServiceGraph(reversed) error = %v", err)
	}
	if !reflect.DeepEqual(forward, reversed) {
		t.Fatalf("graph depends on snapshot order:\nforward=%#v\nreversed=%#v", forward, reversed)
	}
}

func TestValidateServiceGraphRejectsBrokenReferencesAndConstraints(t *testing.T) {
	tests := []struct {
		name string
		edit func([]mib.Instance) []mib.Instance
		want string
	}{
		{
			name: "dangling T-CONT",
			edit: func(snapshot []mib.Instance) []mib.Instance {
				return withoutInstance(snapshot, me.TContClassID, testTCONT)
			},
			want: "missing T-CONT",
		},
		{
			name: "upstream queue belongs to UNI",
			edit: func(snapshot []mib.Instance) []mib.Instance {
				findInstance(snapshot, me.PriorityQueueClassID, 0x8000).Attributes[me.PriorityQueue_RelatedPort] = uint32(testUNI) << 16
				return snapshot
			},
			want: "not T-CONT",
		},
		{
			name: "duplicate Alloc-ID",
			edit: func(snapshot []mib.Instance) []mib.Instance {
				return append(snapshot, mib.Instance{
					Key:        mib.Key{ClassID: me.TContClassID, EntityID: testTCONT + 1},
					Attributes: me.AttributeValueMap{me.TCont_AllocId: uint16(100)},
				})
			},
			want: "Alloc-ID 100 is shared",
		},
		{
			name: "dangling mapper branch",
			edit: func(snapshot []mib.Instance) []mib.Instance {
				findInstance(snapshot, me.Ieee8021PMapperServiceProfileClassID, testMapper).Attributes[me.Ieee8021PMapperServiceProfile_InterworkTpPointerForPBitPriority7] = uint16(0x7777)
				return snapshot
			},
			want: "P-bit 7 references missing GEM IW TP",
		},
		{
			name: "duplicate bridge port number",
			edit: func(snapshot []mib.Instance) []mib.Instance {
				findInstance(snapshot, me.MacBridgePortConfigurationDataClassID, mapperPort).Attributes[me.MacBridgePortConfigurationData_PortNum] = uint8(1)
				return snapshot
			},
			want: "port number 1 is shared",
		},
		{
			name: "bridge GEM belongs to other profile",
			edit: func(snapshot []mib.Instance) []mib.Instance {
				findInstance(snapshot, me.GemInterworkingTerminationPointClassID, testBridgeIW).Attributes[me.GemInterworkingTerminationPoint_ServiceProfilePointer] = uint16(0x7777)
				return snapshot
			},
			want: "missing bridge profile",
		},
		{
			name: "VLAN filter without implicit bridge port",
			edit: func(snapshot []mib.Instance) []mib.Instance {
				return withoutInstance(snapshot, me.MacBridgePortConfigurationDataClassID, uniBridgePort)
			},
			want: "no implicitly linked MAC bridge port",
		},
		{
			name: "reserved VLAN ID",
			edit: func(snapshot []mib.Instance) []mib.Instance {
				list := make([]byte, 24)
				list[0], list[1] = 0x0f, 0xff
				findInstance(snapshot, me.VlanTaggingFilterDataClassID, uniBridgePort).Attributes[me.VlanTaggingFilterData_VlanFilterList] = list
				return snapshot
			},
			want: "reserved VID 4095",
		},
		{
			name: "dangling extended VLAN association",
			edit: func(snapshot []mib.Instance) []mib.Instance {
				findInstance(snapshot, me.ExtendedVlanTaggingOperationConfigurationDataClassID, 0x0600).Attributes[me.ExtendedVlanTaggingOperationConfigurationData_AssociatedMePointer] = uint16(0x7777)
				return snapshot
			},
			want: "references missing",
		},
		{
			name: "delete GAL before GEM IW",
			edit: func(snapshot []mib.Instance) []mib.Instance {
				return withoutInstance(snapshot, me.GalEthernetProfileClassID, testGAL)
			},
			want: "missing GAL Ethernet profile",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateServiceGraph(test.edit(validServiceSnapshot()))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateServiceGraph() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateServiceGraphAllowsUnusedTCONTAndNullMapperBranches(t *testing.T) {
	snapshot := []mib.Instance{
		{
			Key:        mib.Key{ClassID: me.TContClassID, EntityID: testTCONT},
			Attributes: me.AttributeValueMap{me.TCont_AllocId: uint16(nullPointer)},
		},
		mapperInstance(testMapper, nullPointer),
	}
	if err := ValidateServiceGraph(snapshot); err != nil {
		t.Fatalf("ValidateServiceGraph() error = %v", err)
	}
}

func TestOLTCanBuildServiceGraphTransactionByTransaction(t *testing.T) {
	factory, err := model.XG2010G(model.Identity{SerialNumber: "TEST01020304"})
	if err != nil {
		t.Fatalf("model.XG2010G() error = %v", err)
	}
	store, err := mib.NewWithApplier(factory, mib.ApplyFunc(func(change mib.Change) error {
		return ValidateServiceGraph(change.Snapshot)
	}))
	if err != nil {
		t.Fatalf("mib.NewWithApplier() error = %v", err)
	}

	mustSet(t, store, mib.Key{ClassID: me.TContClassID, EntityID: testTCONT}, me.AttributeValueMap{
		me.TCont_AllocId: uint16(100),
	})
	mustCreate(t, store, me.GalEthernetProfileClassID, testGAL, me.AttributeValueMap{
		me.GalEthernetProfile_MaximumGemPayloadSize: uint16(48),
	})
	mustCreate(t, store, me.MacBridgeServiceProfileClassID, testBridge, me.AttributeValueMap{
		me.MacBridgeServiceProfile_SpanningTreeInd:          uint8(0),
		me.MacBridgeServiceProfile_LearningInd:              uint8(1),
		me.MacBridgeServiceProfile_PortBridgingInd:          uint8(0),
		me.MacBridgeServiceProfile_Priority:                 uint16(0x8000),
		me.MacBridgeServiceProfile_MaxAge:                   uint16(20 * 256),
		me.MacBridgeServiceProfile_HelloTime:                uint16(2 * 256),
		me.MacBridgeServiceProfile_ForwardDelay:             uint16(15 * 256),
		me.MacBridgeServiceProfile_UnknownMacAddressDiscard: uint8(0),
	})
	mapperAttributes := mapperInstance(testMapper, nullPointer).Attributes
	mustCreate(t, store, me.Ieee8021PMapperServiceProfileClassID, testMapper, mapperAttributes)
	mustCreate(t, store, me.GemPortNetworkCtpClassID, testMapperGEM,
		gemPortInstance(testMapperGEM, 200).Attributes)
	mustCreate(t, store, me.GemInterworkingTerminationPointClassID, testMapperIW,
		gemIWInstance(testMapperIW, testMapperGEM, 5, testMapper).Attributes)

	mapperBranches := make(me.AttributeValueMap, len(mapperPBitAttributes))
	for _, name := range mapperPBitAttributes {
		mapperBranches[name] = uint16(testMapperIW)
	}
	mustSet(t, store, mib.Key{ClassID: me.Ieee8021PMapperServiceProfileClassID, EntityID: testMapper}, mapperBranches)
	mustCreate(t, store, me.MacBridgePortConfigurationDataClassID, uniBridgePort,
		bridgePortInstance(uniBridgePort, 1, 1, testUNI).Attributes)
	mustCreate(t, store, me.MacBridgePortConfigurationDataClassID, mapperPort,
		bridgePortInstance(mapperPort, 2, 3, testMapper).Attributes)

	vlanList := make([]byte, 24)
	vlanList[1] = 100
	mustCreate(t, store, me.VlanTaggingFilterDataClassID, uniBridgePort, me.AttributeValueMap{
		me.VlanTaggingFilterData_VlanFilterList:   vlanList,
		me.VlanTaggingFilterData_ForwardOperation: uint8(0x10),
		me.VlanTaggingFilterData_NumberOfEntries:  uint8(1),
	})
	mustCreate(t, store, me.ExtendedVlanTaggingOperationConfigurationDataClassID, 0x0600, me.AttributeValueMap{
		me.ExtendedVlanTaggingOperationConfigurationData_AssociationType:     uint8(2),
		me.ExtendedVlanTaggingOperationConfigurationData_AssociatedMePointer: uint16(testUNI),
	})

	graph, err := BuildServiceGraph(store.Snapshot())
	if err != nil {
		t.Fatalf("BuildServiceGraph(final MIB) error = %v", err)
	}
	if len(graph.GEMPorts) != 1 || len(graph.Interworking) != 1 ||
		len(graph.Mappers) != 1 || len(graph.Bridges) != 1 ||
		len(graph.Bridges[0].Ports) != 2 || len(graph.VLANFilters) != 1 ||
		len(graph.ExtendedVLANs) != 1 {
		t.Fatalf("final graph = %#v", graph)
	}
}

func validServiceSnapshot() []mib.Instance {
	vlanList := make([]byte, 24)
	vlanList[1] = 100
	mapper := mapperInstance(testMapper, testMapperIW)
	return []mib.Instance{
		{
			Key: mib.Key{ClassID: me.OnuGClassID, EntityID: 0},
			Attributes: me.AttributeValueMap{
				me.OnuG_TrafficManagementOption: uint8(0),
			},
		},
		{
			Key: mib.Key{ClassID: me.PhysicalPathTerminationPointEthernetUniClassID, EntityID: testUNI},
			Attributes: me.AttributeValueMap{
				me.PhysicalPathTerminationPointEthernetUni_AdministrativeState: uint8(0),
				me.PhysicalPathTerminationPointEthernetUni_OperationalState:    uint8(0),
				me.PhysicalPathTerminationPointEthernetUni_ConfigurationInd:    uint8(4),
			},
		},
		{
			Key:        mib.Key{ClassID: me.TContClassID, EntityID: testTCONT},
			Attributes: me.AttributeValueMap{me.TCont_AllocId: uint16(100)},
		},
		{
			Key: mib.Key{ClassID: me.PriorityQueueClassID, EntityID: 0x8000},
			Attributes: me.AttributeValueMap{
				me.PriorityQueue_RelatedPort: uint32(testTCONT) << 16,
			},
		},
		{
			Key: mib.Key{ClassID: me.PriorityQueueClassID, EntityID: 0},
			Attributes: me.AttributeValueMap{
				me.PriorityQueue_RelatedPort: uint32(testUNI) << 16,
			},
		},
		{Key: mib.Key{ClassID: me.GalEthernetProfileClassID, EntityID: testGAL}, Attributes: me.AttributeValueMap{}},
		{Key: mib.Key{ClassID: me.MacBridgeServiceProfileClassID, EntityID: testBridge}, Attributes: me.AttributeValueMap{}},
		mapper,
		gemPortInstance(testMapperGEM, 200),
		gemPortInstance(testBridgeGEM, 201),
		gemIWInstance(testMapperIW, testMapperGEM, 5, testMapper),
		gemIWInstance(testBridgeIW, testBridgeGEM, 1, testBridge),
		bridgePortInstance(uniBridgePort, 1, 1, testUNI),
		bridgePortInstance(mapperPort, 2, 3, testMapper),
		bridgePortInstance(gemBridgePort, 3, 5, testBridgeIW),
		{
			Key: mib.Key{ClassID: me.VlanTaggingFilterDataClassID, EntityID: uniBridgePort},
			Attributes: me.AttributeValueMap{
				me.VlanTaggingFilterData_VlanFilterList:   vlanList,
				me.VlanTaggingFilterData_ForwardOperation: uint8(0x10),
				me.VlanTaggingFilterData_NumberOfEntries:  uint8(1),
			},
		},
		{
			Key: mib.Key{ClassID: me.ExtendedVlanTaggingOperationConfigurationDataClassID, EntityID: 0x0600},
			Attributes: me.AttributeValueMap{
				me.ExtendedVlanTaggingOperationConfigurationData_AssociationType:                               uint8(2),
				me.ExtendedVlanTaggingOperationConfigurationData_AssociatedMePointer:                           uint16(testUNI),
				me.ExtendedVlanTaggingOperationConfigurationData_ReceivedFrameVlanTaggingOperationTableMaxSize: uint16(64),
				me.ExtendedVlanTaggingOperationConfigurationData_InputTpid:                                     uint16(0x8100),
				me.ExtendedVlanTaggingOperationConfigurationData_OutputTpid:                                    uint16(0x8100),
				me.ExtendedVlanTaggingOperationConfigurationData_DownstreamMode:                                uint8(0),
				me.ExtendedVlanTaggingOperationConfigurationData_EnhancedMode:                                  uint8(0),
				me.ExtendedVlanTaggingOperationConfigurationData_ReceivedFrameVlanTaggingOperationTable: me.TableRows{
					NumRows: 1,
					Rows:    make([]byte, 16),
				},
			},
		},
	}
}

func mapperInstance(entityID, branch uint16) mib.Instance {
	attributes := me.AttributeValueMap{
		me.Ieee8021PMapperServiceProfile_TpPointer:             uint16(nullPointer),
		me.Ieee8021PMapperServiceProfile_UnmarkedFrameOption:   uint8(1),
		me.Ieee8021PMapperServiceProfile_DefaultPBitAssumption: uint8(0),
		me.Ieee8021PMapperServiceProfile_TpType:                uint8(0),
	}
	for _, name := range mapperPBitAttributes {
		attributes[name] = branch
	}
	return mib.Instance{
		Key:        mib.Key{ClassID: me.Ieee8021PMapperServiceProfileClassID, EntityID: entityID},
		Attributes: attributes,
	}
}

func gemPortInstance(entityID, portID uint16) mib.Instance {
	return mib.Instance{
		Key: mib.Key{ClassID: me.GemPortNetworkCtpClassID, EntityID: entityID},
		Attributes: me.AttributeValueMap{
			me.GemPortNetworkCtp_PortId:                              portID,
			me.GemPortNetworkCtp_TContPointer:                        uint16(testTCONT),
			me.GemPortNetworkCtp_Direction:                           uint8(3),
			me.GemPortNetworkCtp_TrafficManagementPointerForUpstream: uint16(0x8000),
			me.GemPortNetworkCtp_PriorityQueuePointerForDownStream:   uint16(0),
			me.GemPortNetworkCtp_EncryptionKeyRing:                   uint8(0),
		},
	}
}

func gemIWInstance(entityID, gemPort uint16, option uint8, service uint16) mib.Instance {
	return mib.Instance{
		Key: mib.Key{ClassID: me.GemInterworkingTerminationPointClassID, EntityID: entityID},
		Attributes: me.AttributeValueMap{
			me.GemInterworkingTerminationPoint_GemPortNetworkCtpConnectivityPointer: gemPort,
			me.GemInterworkingTerminationPoint_InterworkingOption:                   option,
			me.GemInterworkingTerminationPoint_ServiceProfilePointer:                service,
			me.GemInterworkingTerminationPoint_InterworkingTerminationPointPointer:  uint16(0),
			me.GemInterworkingTerminationPoint_GalProfilePointer:                    uint16(testGAL),
		},
	}
}

func bridgePortInstance(entityID uint16, port, tpType uint8, tp uint16) mib.Instance {
	return mib.Instance{
		Key: mib.Key{ClassID: me.MacBridgePortConfigurationDataClassID, EntityID: entityID},
		Attributes: me.AttributeValueMap{
			me.MacBridgePortConfigurationData_BridgeIdPointer: uint16(testBridge),
			me.MacBridgePortConfigurationData_PortNum:         port,
			me.MacBridgePortConfigurationData_TpType:          tpType,
			me.MacBridgePortConfigurationData_TpPointer:       tp,
		},
	}
}

func findInstance(snapshot []mib.Instance, classID me.ClassID, entityID uint16) *mib.Instance {
	for index := range snapshot {
		if snapshot[index].ClassID == classID && snapshot[index].EntityID == entityID {
			return &snapshot[index]
		}
	}
	panic("test instance not found")
}

func withoutInstance(snapshot []mib.Instance, classID me.ClassID, entityID uint16) []mib.Instance {
	filtered := make([]mib.Instance, 0, len(snapshot)-1)
	for _, instance := range snapshot {
		if instance.ClassID != classID || instance.EntityID != entityID {
			filtered = append(filtered, instance)
		}
	}
	return filtered
}

func mustCreate(t *testing.T, store *mib.Store, classID me.ClassID, entityID uint16,
	attributes me.AttributeValueMap) {
	t.Helper()
	if err := store.Create(classID, entityID, attributes); err != nil {
		t.Fatalf("Create(%d/%#x) error = %v", classID, entityID, err)
	}
}

func mustSet(t *testing.T, store *mib.Store, key mib.Key, attributes me.AttributeValueMap) {
	t.Helper()
	if err := store.Set(key, attributes); err != nil {
		t.Fatalf("Set(%d/%#x) error = %v", key.ClassID, key.EntityID, err)
	}
}
