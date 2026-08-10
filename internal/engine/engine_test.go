// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"testing"

	"github.com/google/gopacket"
	omci "github.com/opencord/omci-lib-go/v2"
	me "github.com/opencord/omci-lib-go/v2/generated"
	"github.com/xg2010g/airoha-omci/internal/mib"
)

func TestCreateDuplicateIsReplayedWithoutDoubleMutation(t *testing.T) {
	engine, store := newTestEngine(t)
	request := encodeRequest(t, 1, omci.CreateRequestType, &omci.CreateRequest{
		MeBasePacket: omci.MeBasePacket{
			EntityClass:    me.GalEthernetProfileClassID,
			EntityInstance: 1,
		},
		Attributes: me.AttributeValueMap{
			me.GalEthernetProfile_MaximumGemPayloadSize: uint16(48),
		},
	})

	first, err := engine.Handle(request)
	if err != nil {
		t.Fatalf("Handle(first) error = %v", err)
	}
	second, err := engine.Handle(request)
	if err != nil {
		t.Fatalf("Handle(duplicate) error = %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("duplicate response differs from original")
	}
	if got := store.DataSync(); got != 1 {
		t.Fatalf("DataSync() = %d, want 1", got)
	}

	packet := decodeResponse(t, first)
	response := packet.Layer(omci.LayerTypeCreateResponse).(*omci.CreateResponse)
	if response.Result != me.Success {
		t.Fatalf("Create result = %v, want success", response.Result)
	}
}

func TestMibResetAndUpload(t *testing.T) {
	engine, store := newTestEngine(t)
	if err := store.Create(me.GalEthernetProfileClassID, 1, me.AttributeValueMap{
		me.GalEthernetProfile_MaximumGemPayloadSize: uint16(48),
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	reset := encodeRequest(t, 2, omci.MibResetRequestType, &omci.MibResetRequest{
		MeBasePacket: omci.MeBasePacket{EntityClass: me.OnuDataClassID},
	})
	if _, err := engine.Handle(reset); err != nil {
		t.Fatalf("Handle(MIB reset) error = %v", err)
	}
	if got := store.DataSync(); got != 0 {
		t.Fatalf("DataSync() = %d, want 0", got)
	}

	upload := encodeRequest(t, 3, omci.MibUploadRequestType, &omci.MibUploadRequest{
		MeBasePacket: omci.MeBasePacket{EntityClass: me.OnuDataClassID},
	})
	encoded, err := engine.Handle(upload)
	if err != nil {
		t.Fatalf("Handle(MIB upload) error = %v", err)
	}
	response := decodeResponse(t, encoded).Layer(omci.LayerTypeMibUploadResponse).(*omci.MibUploadResponse)
	if response.NumberOfCommands != 1 {
		t.Fatalf("NumberOfCommands = %d, want 1", response.NumberOfCommands)
	}

	next := encodeRequest(t, 4, omci.MibUploadNextRequestType, &omci.MibUploadNextRequest{
		MeBasePacket:          omci.MeBasePacket{EntityClass: me.OnuDataClassID},
		CommandSequenceNumber: 0,
	})
	encoded, err = engine.Handle(next)
	if err != nil {
		t.Fatalf("Handle(MIB upload next) error = %v", err)
	}
	nextResponse := decodeResponse(t, encoded).Layer(omci.LayerTypeMibUploadNextResponse).(*omci.MibUploadNextResponse)
	if nextResponse.ReportedME.GetClassID() != me.OnuDataClassID {
		t.Fatalf("reported class = %v, want ONU data", nextResponse.ReportedME.GetClassID())
	}
}

func TestGetONUData(t *testing.T) {
	engine, _ := newTestEngine(t)
	request := encodeRequest(t, 5, omci.GetRequestType, &omci.GetRequest{
		MeBasePacket:  omci.MeBasePacket{EntityClass: me.OnuDataClassID},
		AttributeMask: 0x8000,
	})
	encoded, err := engine.Handle(request)
	if err != nil {
		t.Fatalf("Handle(Get) error = %v", err)
	}
	response := decodeResponse(t, encoded).Layer(omci.LayerTypeGetResponse).(*omci.GetResponse)
	if response.Result != me.Success {
		t.Fatalf("Get result = %v, want success", response.Result)
	}
	if got := response.Attributes[me.OnuData_MibDataSync]; got != uint8(0) {
		t.Fatalf("MIB data sync = %#v, want 0", got)
	}
}

func TestGetReturnsSupportedAttributesAlongsideFailureMask(t *testing.T) {
	engine, _ := newTestEngine(t)
	request := encodeRequest(t, 6, omci.GetRequestType, &omci.GetRequest{
		MeBasePacket:  omci.MeBasePacket{EntityClass: me.OnuDataClassID},
		AttributeMask: 0xc000,
	})
	encoded, err := engine.Handle(request)
	if err != nil {
		t.Fatalf("Handle(Get) error = %v", err)
	}
	response := decodeResponse(t, encoded).Layer(omci.LayerTypeGetResponse).(*omci.GetResponse)
	if response.Result != me.AttributeFailure || response.UnsupportedAttributeMask != 0x4000 {
		t.Fatalf("Get result = %v unsupported=%#x, want AttributeFailure/0x4000", response.Result, response.UnsupportedAttributeMask)
	}
	if got := response.Attributes[me.OnuData_MibDataSync]; got != uint8(0) {
		t.Fatalf("MIB data sync = %#v, want 0", got)
	}
}

func newTestEngine(t *testing.T) (*Engine, *mib.Store) {
	t.Helper()
	store, err := mib.New([]mib.Instance{{
		Key: mib.Key{ClassID: me.OnuDataClassID, EntityID: 0},
		Attributes: me.AttributeValueMap{
			me.OnuData_MibDataSync: uint8(0),
		},
	}})
	if err != nil {
		t.Fatalf("mib.New() error = %v", err)
	}
	return New(store), store
}

func encodeRequest(t *testing.T, transactionID uint16, messageType omci.MessageType, payload gopacket.SerializableLayer) []byte {
	t.Helper()
	header := &omci.OMCI{
		TransactionID:    transactionID,
		MessageType:      messageType,
		DeviceIdentifier: omci.BaselineIdent,
	}
	buffer := gopacket.NewSerializeBuffer()
	if err := gopacket.SerializeLayers(buffer, gopacket.SerializeOptions{FixLengths: true}, header, payload); err != nil {
		t.Fatalf("SerializeLayers() error = %v", err)
	}
	return buffer.Bytes()
}

func decodeResponse(t *testing.T, encoded []byte) gopacket.Packet {
	t.Helper()
	packet := gopacket.NewPacket(encoded, omci.LayerTypeOMCI, gopacket.Default)
	if err := packet.ErrorLayer(); err != nil {
		t.Fatalf("decode response error = %v\nframe = %x", err.Error(), encoded)
	}
	return packet
}
