// SPDX-License-Identifier: Apache-2.0

package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriterPublishesJSONAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run", "status.json")
	writer := NewWriter(path)
	want := Snapshot{State: "online", Interface: "omci", StartedAt: time.Unix(1, 0).UTC(), RXFrames: 3}
	if err := writer.Write(want); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var got Snapshot
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got.State != want.State || got.Interface != want.Interface || got.RXFrames != want.RXFrames {
		t.Fatalf("snapshot = %#v, want %#v", got, want)
	}
}

func TestXGSOMCIEvidenceWriterPublishesTrustedTransportSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run", "xgs-omci-evidence.json")
	writer := NewXGSOMCIEvidenceWriter(path)
	want := XGSOMCIEvidence{
		Version: 4, Complete: false, Semantics: "trusted-transport-kernel-instance-session",
		PONMode: "xgspon", StartedAt: time.Unix(1, 0).UTC(),
		KernelInstanceGeneration: 6, KernelSessionGeneration: 7,
		DispatcherGeneration: 8,
		BaselineMessages:     11, ExtendedMessages: 12,
	}
	if err := writer.Write(want); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var got XGSOMCIEvidence
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got.Version != want.Version || got.Complete || got.Semantics != want.Semantics ||
		got.PONMode != want.PONMode ||
		got.KernelInstanceGeneration != want.KernelInstanceGeneration ||
		got.KernelSessionGeneration != want.KernelSessionGeneration ||
		got.DispatcherGeneration != want.DispatcherGeneration ||
		got.BaselineMessages != want.BaselineMessages || got.ExtendedMessages != want.ExtendedMessages {
		t.Fatalf("evidence snapshot = %#v, want %#v", got, want)
	}
}
