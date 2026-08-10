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

// ExecApplier sends one complete candidate MIB snapshot to a fixed helper.
// No shell is involved. A non-zero helper exit rejects the OMCI mutation.
type ExecApplier struct {
	Path    string
	Timeout time.Duration
}

func (a ExecApplier) Apply(change mib.Change) error {
	if a.Path == "" {
		return fmt.Errorf("platform apply helper is empty")
	}
	if err := ValidateServiceGraph(change.Snapshot); err != nil {
		return err
	}
	timeout := a.Timeout
	if timeout <= 0 {
		timeout = defaultApplyTimeout
	}
	payload, err := json.Marshal(change)
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
