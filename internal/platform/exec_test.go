// SPDX-License-Identifier: Apache-2.0

package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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

	change := mib.Change{Operation: mib.OperationReset, MIBDataSync: 7}
	if err := (ExecApplier{Path: helper}).Apply(change); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	payload, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("ReadFile(output) error = %v", err)
	}
	if !strings.Contains(string(payload), `"operation":"reset"`) ||
		!strings.Contains(string(payload), `"mib_data_sync":7`) {
		t.Fatalf("payload = %s", payload)
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
