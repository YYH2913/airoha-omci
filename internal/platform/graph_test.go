// SPDX-License-Identifier: Apache-2.0

package platform

import (
	"strings"
	"testing"

	me "github.com/opencord/omci-lib-go/v2/generated"
	"github.com/xg2010g/airoha-omci/internal/mib"
)

func TestValidateServiceGraphAcceptsTContGemChain(t *testing.T) {
	snapshot := []mib.Instance{
		{Key: mib.Key{ClassID: me.TContClassID, EntityID: 0x8001}, Attributes: me.AttributeValueMap{
			me.TCont_AllocId: uint16(100),
		}},
		{Key: mib.Key{ClassID: me.GemPortNetworkCtpClassID, EntityID: 1}, Attributes: me.AttributeValueMap{
			me.GemPortNetworkCtp_PortId:       uint16(200),
			me.GemPortNetworkCtp_TContPointer: uint16(0x8001),
			me.GemPortNetworkCtp_Direction:    uint8(3),
		}},
		{Key: mib.Key{ClassID: me.GemInterworkingTerminationPointClassID, EntityID: 1}, Attributes: me.AttributeValueMap{
			me.GemInterworkingTerminationPoint_GemPortNetworkCtpConnectivityPointer: uint16(1),
		}},
	}
	if err := ValidateServiceGraph(snapshot); err != nil {
		t.Fatalf("ValidateServiceGraph() error = %v", err)
	}
}

func TestValidateServiceGraphRejectsDanglingTCont(t *testing.T) {
	snapshot := []mib.Instance{{
		Key: mib.Key{ClassID: me.GemPortNetworkCtpClassID, EntityID: 1},
		Attributes: me.AttributeValueMap{
			me.GemPortNetworkCtp_PortId:       uint16(200),
			me.GemPortNetworkCtp_TContPointer: uint16(0x8001),
			me.GemPortNetworkCtp_Direction:    uint8(3),
		},
	}}
	err := ValidateServiceGraph(snapshot)
	if err == nil || !strings.Contains(err.Error(), "missing T-CONT") {
		t.Fatalf("ValidateServiceGraph() error = %v, want missing T-CONT", err)
	}
}
