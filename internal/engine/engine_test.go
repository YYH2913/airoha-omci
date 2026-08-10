// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"encoding/binary"
	"errors"
	"testing"
	"time"

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

func TestUnknownEntityGetsProtocolErrorResponse(t *testing.T) {
	engine, _ := newTestEngine(t)
	request := encodeRequest(t, 7, omci.GetRequestType, &omci.GetRequest{
		MeBasePacket:  omci.MeBasePacket{EntityClass: me.OnuDataClassID},
		AttributeMask: 0x8000,
	})
	binary.BigEndian.PutUint16(request[4:6], 0xfffe)

	encoded, err := engine.Handle(request)
	if err != nil {
		t.Fatalf("Handle(unknown class) error = %v", err)
	}
	if got := omci.MessageType(encoded[2]); got != omci.GetResponseType {
		t.Fatalf("message type = %v, want Get response", got)
	}
	if got := me.Results(encoded[8]); got != me.UnknownEntity {
		t.Fatalf("result = %v, want UnknownEntity", got)
	}
}

func TestPlatformFailureReturnsProcessingErrorWithoutMutation(t *testing.T) {
	store, err := mib.NewWithApplier([]mib.Instance{{
		Key: mib.Key{ClassID: me.OnuDataClassID, EntityID: 0},
		Attributes: me.AttributeValueMap{
			me.OnuData_MibDataSync: uint8(0),
		},
	}}, mib.ApplyFunc(func(mib.Change) error { return errors.New("apply failed") }))
	if err != nil {
		t.Fatalf("NewWithApplier() error = %v", err)
	}
	protocol := New(store)
	request := encodeRequest(t, 8, omci.CreateRequestType, &omci.CreateRequest{
		MeBasePacket: omci.MeBasePacket{
			EntityClass:    me.GalEthernetProfileClassID,
			EntityInstance: 1,
		},
		Attributes: me.AttributeValueMap{
			me.GalEthernetProfile_MaximumGemPayloadSize: uint16(48),
		},
	})
	encoded, err := protocol.Handle(request)
	if err != nil {
		t.Fatalf("Handle(Create) error = %v", err)
	}
	response := decodeResponse(t, encoded).Layer(omci.LayerTypeCreateResponse).(*omci.CreateResponse)
	if response.Result != me.ProcessingError {
		t.Fatalf("Create result = %v, want ProcessingError", response.Result)
	}
	if store.DataSync() != 0 || len(store.Snapshot()) != 1 {
		t.Fatalf("rejected platform apply changed MIB: sync=%d MEs=%d", store.DataSync(), len(store.Snapshot()))
	}
}

func TestGetNextUsesStableTableSnapshot(t *testing.T) {
	rows := make([]byte, 32)
	for index := range rows {
		rows[index] = byte(index + 1)
	}
	store, err := mib.New([]mib.Instance{{
		Key: mib.Key{ClassID: me.ExtendedVlanTaggingOperationConfigurationDataClassID, EntityID: 1},
		Attributes: me.AttributeValueMap{
			me.ExtendedVlanTaggingOperationConfigurationData_AssociationType:                               uint8(2),
			me.ExtendedVlanTaggingOperationConfigurationData_ReceivedFrameVlanTaggingOperationTableMaxSize: uint16(8),
			me.ExtendedVlanTaggingOperationConfigurationData_InputTpid:                                     uint16(0x8100),
			me.ExtendedVlanTaggingOperationConfigurationData_OutputTpid:                                    uint16(0x8100),
			me.ExtendedVlanTaggingOperationConfigurationData_DownstreamMode:                                uint8(0),
			me.ExtendedVlanTaggingOperationConfigurationData_ReceivedFrameVlanTaggingOperationTable: me.TableRows{
				NumRows: 2,
				Rows:    rows,
			},
		},
	}})
	if err != nil {
		t.Fatalf("mib.New() error = %v", err)
	}
	protocol := New(store)
	get := encodeRequest(t, 9, omci.GetRequestType, &omci.GetRequest{
		MeBasePacket: omci.MeBasePacket{
			EntityClass:    me.ExtendedVlanTaggingOperationConfigurationDataClassID,
			EntityInstance: 1,
		},
		AttributeMask: 0x0400,
	})
	encoded, err := protocol.Handle(get)
	if err != nil {
		t.Fatalf("Handle(Get table) error = %v", err)
	}
	getResponse := decodeResponse(t, encoded).Layer(omci.LayerTypeGetResponse).(*omci.GetResponse)
	if got := getResponse.Attributes[me.ExtendedVlanTaggingOperationConfigurationData_ReceivedFrameVlanTaggingOperationTable]; got != uint32(len(rows)) {
		t.Fatalf("table size = %#v, want %d", got, len(rows))
	}

	next := encodeRequest(t, 10, omci.GetNextRequestType, &omci.GetNextRequest{
		MeBasePacket: omci.MeBasePacket{
			EntityClass:    me.ExtendedVlanTaggingOperationConfigurationDataClassID,
			EntityInstance: 1,
		},
		AttributeMask:  0x0400,
		SequenceNumber: 0,
	})
	encoded, err = protocol.Handle(next)
	if err != nil {
		t.Fatalf("Handle(GetNext) error = %v", err)
	}
	nextResponse := decodeResponse(t, encoded).Layer(omci.LayerTypeGetNextResponse).(*omci.GetNextResponse)
	got, ok := nextResponse.Attributes[me.ExtendedVlanTaggingOperationConfigurationData_ReceivedFrameVlanTaggingOperationTable].([]byte)
	if !ok || len(got) < omci.MaxAttributeGetNextBaselineLength ||
		string(got[:omci.MaxAttributeGetNextBaselineLength]) != string(rows[:omci.MaxAttributeGetNextBaselineLength]) {
		t.Fatalf("GetNext table chunk = %x", got)
	}
}

func TestExtendedMibUploadPacksMultipleManagedEntities(t *testing.T) {
	snapshot := make([]mib.Instance, 12)
	for index := range snapshot {
		snapshot[index] = mib.Instance{
			Key: mib.Key{ClassID: me.OnuDataClassID, EntityID: uint16(index)},
			Attributes: me.AttributeValueMap{
				me.OnuData_MibDataSync: uint8(index),
			},
		}
	}
	commands, err := buildUpload(snapshot, omci.ExtendedIdent)
	if err != nil {
		t.Fatalf("buildUpload() error = %v", err)
	}
	if len(commands) != 1 || len(commands[0]) != len(snapshot) {
		t.Fatalf("extended upload commands = %d MEs=%d, want 1/%d", len(commands), len(commands[0]), len(snapshot))
	}
}

func TestGetAllAlarmsUsesStableSnapshot(t *testing.T) {
	protocol, _ := newTestEngine(t)
	var bitmap [28]byte
	bitmap[0] = 0x80
	protocol.SetAlarm(mib.Key{ClassID: me.AniGClassID, EntityID: 0x8001}, bitmap)

	request := encodeRequest(t, 11, omci.GetAllAlarmsRequestType, &omci.GetAllAlarmsRequest{
		MeBasePacket:       omci.MeBasePacket{EntityClass: me.OnuDataClassID},
		AlarmRetrievalMode: 0,
	})
	encoded, err := protocol.Handle(request)
	if err != nil {
		t.Fatalf("Handle(GetAllAlarms) error = %v", err)
	}
	response := decodeResponse(t, encoded).Layer(omci.LayerTypeGetAllAlarmsResponse).(*omci.GetAllAlarmsResponse)
	if response.NumberOfCommands != 1 {
		t.Fatalf("NumberOfCommands = %d, want 1", response.NumberOfCommands)
	}

	next := encodeRequest(t, 12, omci.GetAllAlarmsNextRequestType, &omci.GetAllAlarmsNextRequest{
		MeBasePacket:          omci.MeBasePacket{EntityClass: me.OnuDataClassID},
		CommandSequenceNumber: 0,
	})
	encoded, err = protocol.Handle(next)
	if err != nil {
		t.Fatalf("Handle(GetAllAlarmsNext) error = %v", err)
	}
	nextResponse := decodeResponse(t, encoded).Layer(omci.LayerTypeGetAllAlarmsNextResponse).(*omci.GetAllAlarmsNextResponse)
	if nextResponse.AlarmEntityClass != me.AniGClassID ||
		nextResponse.AlarmEntityInstance != 0x8001 || nextResponse.AlarmBitMap != bitmap {
		t.Fatalf("alarm response = %#v", nextResponse)
	}
}

type recordingController struct {
	timestamp time.Time
	reboot    uint8
}

func (c *recordingController) SynchronizeTime(value time.Time) error {
	c.timestamp = value
	return nil
}

func (c *recordingController) Reboot(condition uint8) error {
	c.reboot = condition
	return nil
}

func TestSynchronizeTimeHandlesBaselineLibraryDecodeFailure(t *testing.T) {
	_, store := newTestEngine(t)
	controller := &recordingController{}
	protocol := NewWithController(store, controller)
	request := encodeRequest(t, 13, omci.SynchronizeTimeRequestType, &omci.SynchronizeTimeRequest{
		MeBasePacket: omci.MeBasePacket{EntityClass: me.OnuGClassID},
		Year:         2026,
		Month:        8,
		Day:          10,
		Hour:         12,
		Minute:       34,
		Second:       56,
	})
	encoded, err := protocol.Handle(request)
	if err != nil {
		t.Fatalf("Handle(SynchronizeTime) error = %v", err)
	}
	if got := me.Results(encoded[8]); got != me.Success {
		t.Fatalf("result = %v, want Success", got)
	}
	want := time.Date(2026, 8, 10, 12, 34, 56, 0, time.UTC)
	if !controller.timestamp.Equal(want) {
		t.Fatalf("timestamp = %v, want %v", controller.timestamp, want)
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
