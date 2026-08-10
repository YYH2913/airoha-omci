// SPDX-License-Identifier: Apache-2.0

package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExecControllerEncodesActions(t *testing.T) {
	directory := t.TempDir()
	output := filepath.Join(directory, "control.json")
	helper := filepath.Join(directory, "control")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\ncat > "+output+"\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(helper) error = %v", err)
	}
	controller := ExecController{Path: helper}
	if err := controller.SynchronizeTime(time.Unix(1234, 0)); err != nil {
		t.Fatalf("SynchronizeTime() error = %v", err)
	}
	payload, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(payload), `"action":"synchronize-time"`) ||
		!strings.Contains(string(payload), `"unix_time":1234`) {
		t.Fatalf("payload = %s", payload)
	}
}
