// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"encoding/binary"
	"errors"
	"net/netip"
	"testing"

	omci "github.com/opencord/omci-lib-go/v2"
	me "github.com/opencord/omci-lib-go/v2/generated"
	"github.com/xg2010g/airoha-omci/internal/mib"
	"github.com/xg2010g/airoha-omci/internal/multicast"
)

type recordingMulticastController struct {
	*recordingController
	entity  uint16
	monitor multicast.Monitor
	err     error
}

func (c *recordingMulticastController) SubscriberMonitor(entityID uint16) (multicast.Monitor, error) {
	c.entity = entityID
	return c.monitor, c.err
}

func TestGetMulticastSubscriberMonitorUsesLiveBackend(t *testing.T) {
	const entityID = 0x0500
	controller := &recordingMulticastController{
		recordingController: &recordingController{},
		monitor: multicast.Monitor{
			CurrentBandwidth: 3_000_000, JoinMessages: 17, BandwidthExceeded: 2,
			Groups: []multicast.ActiveGroup{{
				Source:  netip.MustParseAddr("198.51.100.1"),
				Group:   netip.MustParseAddr("239.1.2.3"),
				Client:  netip.MustParseAddr("192.0.2.10"),
				ANIVLAN: 100, ImputedBandwidth: 3_000_000, TimeSinceJoin: 45,
			}},
		},
	}
	protocol := newMulticastMonitorEngine(t, controller, entityID)
	request := encodeRequest(t, 0x700, omci.GetRequestType, &omci.GetRequest{
		MeBasePacket: omci.MeBasePacket{
			EntityClass: me.MulticastSubscriberMonitorClassID, EntityInstance: entityID,
		},
		AttributeMask: 0x7000,
	})
	encoded, err := protocol.Handle(request)
	if err != nil {
		t.Fatalf("Handle(Get multicast counters) error = %v", err)
	}
	response := decodeResponse(t, encoded).Layer(omci.LayerTypeGetResponse).(*omci.GetResponse)
	if response.Result != me.Success || controller.entity != entityID ||
		response.Attributes[me.MulticastSubscriberMonitor_CurrentMulticastBandwidth] != uint32(3_000_000) ||
		response.Attributes[me.MulticastSubscriberMonitor_JoinMessagesCounter] != uint32(17) ||
		response.Attributes[me.MulticastSubscriberMonitor_BandwidthExceededCounter] != uint32(2) {
		t.Fatalf("multicast monitor response = %#v", response)
	}

	request = encodeRequest(t, 0x701, omci.GetRequestType, &omci.GetRequest{
		MeBasePacket: omci.MeBasePacket{
			EntityClass: me.MulticastSubscriberMonitorClassID, EntityInstance: entityID,
		},
		AttributeMask: multicastMonitorIPv4TableMask,
	})
	encoded, err = protocol.Handle(request)
	if err != nil {
		t.Fatalf("Handle(Get multicast table) error = %v", err)
	}
	response = decodeResponse(t, encoded).Layer(omci.LayerTypeGetResponse).(*omci.GetResponse)
	if got := response.Attributes[me.MulticastSubscriberMonitor_Ipv4ActiveGroupListTable]; got != uint32(24) {
		t.Fatalf("IPv4 active-group table size = %#v, want 24", got)
	}

	next := encodeRequest(t, 0x702, omci.GetNextRequestType, &omci.GetNextRequest{
		MeBasePacket: omci.MeBasePacket{
			EntityClass: me.MulticastSubscriberMonitorClassID, EntityInstance: entityID,
		},
		AttributeMask: multicastMonitorIPv4TableMask, SequenceNumber: 0,
	})
	encoded, err = protocol.Handle(next)
	if err != nil {
		t.Fatalf("Handle(GetNext multicast table) error = %v", err)
	}
	nextResponse := decodeResponse(t, encoded).Layer(omci.LayerTypeGetNextResponse).(*omci.GetNextResponse)
	row, ok := nextResponse.Attributes[me.MulticastSubscriberMonitor_Ipv4ActiveGroupListTable].([]byte)
	if !ok || len(row) < 24 || binary.BigEndian.Uint16(row[0:2]) != 100 ||
		netip.AddrFrom4([4]byte(row[2:6])) != netip.MustParseAddr("198.51.100.1") ||
		netip.AddrFrom4([4]byte(row[6:10])) != netip.MustParseAddr("239.1.2.3") ||
		binary.BigEndian.Uint32(row[10:14]) != 3_000_000 ||
		netip.AddrFrom4([4]byte(row[14:18])) != netip.MustParseAddr("192.0.2.10") ||
		binary.BigEndian.Uint32(row[18:22]) != 45 {
		t.Fatalf("IPv4 active-group row = %x", row)
	}
}

func TestMulticastMonitorIPv6TableSupportsMixedClientAddress(t *testing.T) {
	_, ipv6, err := multicastMonitorTables([]multicast.ActiveGroup{{
		Source: netip.MustParseAddr("2001:db8::1"), Group: netip.MustParseAddr("ff3e::100"),
		Client: netip.MustParseAddr("192.0.2.10"), ANIVLAN: 200,
		ImputedBandwidth: 1234, TimeSinceJoin: 90,
	}})
	if err != nil {
		t.Fatalf("multicastMonitorTables() error = %v", err)
	}
	if ipv6.NumRows != 1 || len(ipv6.Rows) != 58 ||
		binary.BigEndian.Uint16(ipv6.Rows[0:2]) != 200 ||
		netip.AddrFrom16([16]byte(ipv6.Rows[2:18])) != netip.MustParseAddr("2001:db8::1") ||
		netip.AddrFrom16([16]byte(ipv6.Rows[18:34])) != netip.MustParseAddr("ff3e::100") ||
		binary.BigEndian.Uint32(ipv6.Rows[34:38]) != 1234 ||
		netip.AddrFrom4([4]byte(ipv6.Rows[50:54])) != netip.MustParseAddr("192.0.2.10") ||
		binary.BigEndian.Uint32(ipv6.Rows[54:58]) != 90 {
		t.Fatalf("IPv6 active-group rows = %x", ipv6.Rows)
	}
}

func TestGetMulticastMonitorReportsBackendFailure(t *testing.T) {
	const entityID = 0x0500
	controller := &recordingMulticastController{
		recordingController: &recordingController{}, err: errors.New("runtime unavailable"),
	}
	protocol := newMulticastMonitorEngine(t, controller, entityID)
	request := encodeRequest(t, 0x710, omci.GetRequestType, &omci.GetRequest{
		MeBasePacket: omci.MeBasePacket{
			EntityClass: me.MulticastSubscriberMonitorClassID, EntityInstance: entityID,
		},
		AttributeMask: multicastMonitorCurrentBandwidthMask,
	})
	encoded, err := protocol.Handle(request)
	if err != nil {
		t.Fatalf("Handle(Get failed multicast monitor) error = %v", err)
	}
	response := decodeResponse(t, encoded).Layer(omci.LayerTypeGetResponse).(*omci.GetResponse)
	if response.Result != me.ProcessingError || response.AttributeMask != 0 {
		t.Fatalf("failed multicast monitor response = %#v", response)
	}
}

func newMulticastMonitorEngine(t *testing.T, controller Controller, entityID uint16) *Engine {
	t.Helper()
	store, err := mib.New([]mib.Instance{{
		Key: mib.Key{ClassID: me.MulticastSubscriberMonitorClassID, EntityID: entityID},
		Attributes: me.AttributeValueMap{
			me.MulticastSubscriberMonitor_MeType:                    uint8(0),
			me.MulticastSubscriberMonitor_CurrentMulticastBandwidth: uint32(0),
			me.MulticastSubscriberMonitor_JoinMessagesCounter:       uint32(0),
			me.MulticastSubscriberMonitor_BandwidthExceededCounter:  uint32(0),
			me.MulticastSubscriberMonitor_Ipv4ActiveGroupListTable:  me.TableRows{},
			me.MulticastSubscriberMonitor_Ipv6ActiveGroupListTable:  me.TableRows{},
		},
	}})
	if err != nil {
		t.Fatalf("mib.New() error = %v", err)
	}
	return NewWithController(store, controller)
}
