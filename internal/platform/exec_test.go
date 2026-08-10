// SPDX-License-Identifier: Apache-2.0

package platform

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	me "github.com/opencord/omci-lib-go/v2/generated"
	"github.com/xg2010g/airoha-omci/internal/mib"
)

func TestExecApplierPassesCandidateAsJSON(t *testing.T) {
	directory := t.TempDir()
	output := filepath.Join(directory, "change.json")
	helper := filepath.Join(directory, "apply")
	script := "#!/bin/sh\ncat > " + output + "\n"
	if err := os.WriteFile(helper, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(helper) error = %v", err)
	}

	change := mib.Change{
		Operation:   mib.OperationReset,
		MIBDataSync: 7,
		Snapshot: []mib.Instance{{
			Key:        mib.Key{ClassID: me.TContClassID, EntityID: 0x8001},
			Attributes: me.AttributeValueMap{me.TCont_AllocId: uint16(100)},
		}},
	}
	if err := (ExecApplier{Path: helper}).Apply(change); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	payload, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("ReadFile(output) error = %v", err)
	}
	var request ApplyRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		t.Fatalf("Unmarshal(payload) error = %v", err)
	}
	if request.Version != ApplyABIVersion || request.Operation != mib.OperationReset ||
		request.MIBDataSync != 7 || len(request.Service.TCONTs) != 1 ||
		request.Service.TCONTs[0].AllocID != 100 {
		t.Fatalf("request = %#v", request)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatalf("Unmarshal(raw payload) error = %v", err)
	}
	if _, exists := raw["snapshot"]; exists {
		t.Fatalf("platform payload leaked raw MIB snapshot: %s", payload)
	}
}

func TestExecApplierReportsHelperFailure(t *testing.T) {
	helper := filepath.Join(t.TempDir(), "apply")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\necho rejected >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(helper) error = %v", err)
	}
	err := (ExecApplier{Path: helper}).Apply(mib.Change{})
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("Apply() error = %v, want helper detail", err)
	}
}
