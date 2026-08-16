// SPDX-License-Identifier: Apache-2.0

package status

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/xg2010g/airoha-omci/internal/pon"
)

type Snapshot struct {
	State                string    `json:"state"`
	Interface            string    `json:"interface"`
	PONMode              pon.Mode  `json:"pon_mode"`
	StartedAt            time.Time `json:"started_at"`
	UpdatedAt            time.Time `json:"updated_at"`
	MIBDataSync          uint8     `json:"mib_data_sync"`
	MIBEntries           int       `json:"mib_entries"`
	PlatformBackend      string    `json:"platform_backend"`
	SoftwareBackend      string    `json:"software_backend"`
	SoftwarePhase        string    `json:"software_phase"`
	SoftwareImageID      uint16    `json:"software_image_id"`
	SoftwareBytes        uint32    `json:"software_bytes"`
	SoftwareImageSize    uint32    `json:"software_image_size"`
	SoftwareImageHash    string    `json:"software_image_hash,omitempty"`
	RXFrames             uint64    `json:"rx_frames"`
	TXFrames             uint64    `json:"tx_frames"`
	DecodeErrors         uint64    `json:"decode_errors"`
	TransportErrors      uint64    `json:"transport_errors"`
	EventErrors          uint64    `json:"event_errors"`
	PerformanceErrors    uint64    `json:"performance_errors"`
	NotificationFrames   uint64    `json:"notification_frames"`
	LastTransactionID    uint16    `json:"last_transaction_id"`
	LastMessageType      uint8     `json:"last_message_type"`
	LastNotificationType uint8     `json:"last_notification_type"`
	LastError            string    `json:"last_error,omitempty"`
}

// XGSOMCIEvidence is a runtime-only diagnostic snapshot of downstream OMCI
// frames delivered to the application after trusted XGS OMCC validation. It
// counts the SDK receive boundary independently of the request result and is
// deliberately not a standalone G.988 performance-management counter backend.
type XGSOMCIEvidence struct {
	Version                  uint8     `json:"version"`
	Complete                 bool      `json:"complete"`
	Semantics                string    `json:"semantics"`
	PONMode                  pon.Mode  `json:"pon_mode"`
	StartedAt                time.Time `json:"started_at"`
	UpdatedAt                time.Time `json:"updated_at"`
	KernelInstanceGeneration uint64    `json:"kernel_instance_generation"`
	KernelSessionGeneration  uint64    `json:"kernel_session_generation"`
	DispatcherGeneration     uint64    `json:"dispatcher_generation"`
	BaselineMessages         uint64    `json:"baseline_messages"`
	ExtendedMessages         uint64    `json:"extended_messages"`
}

type Writer struct {
	path string
}

func NewWriter(path string) *Writer {
	return &Writer{path: path}
}

func (w *Writer) Write(snapshot Snapshot) error {
	snapshot.UpdatedAt = time.Now().UTC()
	return writeJSONAtomically(w.path, snapshot)
}

type XGSOMCIEvidenceWriter struct {
	path string
}

func NewXGSOMCIEvidenceWriter(path string) *XGSOMCIEvidenceWriter {
	return &XGSOMCIEvidenceWriter{path: path}
}

func (w *XGSOMCIEvidenceWriter) Write(snapshot XGSOMCIEvidence) error {
	snapshot.UpdatedAt = time.Now().UTC()
	return writeJSONAtomically(w.path, snapshot)
}

func writeJSONAtomically(path string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode OMCI status: %w", err)
	}
	encoded = append(encoded, '\n')

	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create OMCI status directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".omci-status-*")
	if err != nil {
		return fmt.Errorf("create OMCI status temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(encoded); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write OMCI status: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close OMCI status: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish OMCI status: %w", err)
	}
	return nil
}
