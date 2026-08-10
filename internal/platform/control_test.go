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

func TestExecControllerReadsOpticalDiagnostics(t *testing.T) {
	directory := t.TempDir()
	requestPath := filepath.Join(directory, "request.json")
	helper := filepath.Join(directory, "control")
	script := "#!/bin/sh\ncat > " + requestPath + "\n" +
		"printf '%s\\n' '{\"temperature\":62976,\"supply_voltage\":33000,\"laser_bias_current\":2500,\"transmit_power\":10000,\"receive_power\":10}'\n"
	if err := os.WriteFile(helper, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(helper) error = %v", err)
	}
	diagnostics, err := (ExecController{Path: helper}).OpticalLineSupervision()
	if err != nil {
		t.Fatalf("OpticalLineSupervision() error = %v", err)
	}
	if diagnostics.PowerFeedVoltage != 165 || diagnostics.ReceivedOpticalPower != 0 ||
		diagnostics.MeanOpticalLaunch != 15000 || diagnostics.LaserBiasCurrent != 2500 ||
		diagnostics.Temperature != 0xf600 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	payload, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatalf("ReadFile(request) error = %v", err)
	}
	if !strings.Contains(string(payload), `"action":"optical-line-supervision"`) {
		t.Fatalf("request = %s", payload)
	}
}

func TestExecControllerRejectsIncompleteOpticalDiagnostics(t *testing.T) {
	helper := filepath.Join(t.TempDir(), "control")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf '%s\\n' '{\"temperature\":0}'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(helper) error = %v", err)
	}
	if _, err := (ExecController{Path: helper}).OpticalLineSupervision(); err == nil ||
		!strings.Contains(err.Error(), "required field is missing") {
		t.Fatalf("OpticalLineSupervision() error = %v", err)
	}
}
