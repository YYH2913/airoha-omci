// SPDX-License-Identifier: Apache-2.0

package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/xg2010g/airoha-omci/internal/mib"
)

const defaultApplyTimeout = 10 * time.Second

const ApplyABIVersion = 4

// ApplyRequest is the versioned boundary between the G.988 engine and a
// privileged platform helper. The helper consumes resolved connectivity, not
// raw managed-entity attributes.
type ApplyRequest struct {
	Version     uint8         `json:"version"`
	Operation   mib.Operation `json:"operation"`
	MIBDataSync uint8         `json:"mib_data_sync"`
	Service     ServiceGraph  `json:"service_graph"`
}

// ExecApplier resolves and sends one complete candidate service graph to a
// fixed helper. No shell is involved. A non-zero helper exit rejects the OMCI
// mutation.
type ExecApplier struct {
	Path    string
	Timeout time.Duration
}

func (a ExecApplier) Apply(change mib.Change) error {
	if a.Path == "" {
		return fmt.Errorf("platform apply helper is empty")
	}
	graph, err := BuildServiceGraph(change.Snapshot)
	if err != nil {
		return err
	}
	timeout := a.Timeout
	if timeout <= 0 {
		timeout = defaultApplyTimeout
	}
	payload, err := json.Marshal(ApplyRequest{
		Version:     ApplyABIVersion,
		Operation:   change.Operation,
		MIBDataSync: change.MIBDataSync,
		Service:     graph,
	})
	if err != nil {
		return fmt.Errorf("encode platform change: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, a.Path)
	command.Stdin = bytes.NewReader(payload)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return fmt.Errorf("platform helper timed out after %s: %w", timeout, ctx.Err())
	}
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			return fmt.Errorf("platform helper: %w", err)
		}
		return fmt.Errorf("platform helper: %w: %s", err, detail)
	}
	return nil
}
