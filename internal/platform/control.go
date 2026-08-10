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
)

type ExecController struct {
	Path    string
	Timeout time.Duration
}

type controlRequest struct {
	Action          string `json:"action"`
	UnixTime        int64  `json:"unix_time,omitempty"`
	RebootCondition uint8  `json:"reboot_condition,omitempty"`
}

func (c ExecController) SynchronizeTime(value time.Time) error {
	return c.execute(controlRequest{Action: "synchronize-time", UnixTime: value.Unix()})
}

func (c ExecController) Reboot(condition uint8) error {
	return c.execute(controlRequest{Action: "reboot", RebootCondition: condition})
}

func (c ExecController) execute(request controlRequest) error {
	if c.Path == "" {
		return fmt.Errorf("platform control helper is empty")
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = defaultApplyTimeout
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode platform control: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, c.Path)
	command.Stdin = bytes.NewReader(payload)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return fmt.Errorf("platform control timed out after %s: %w", timeout, ctx.Err())
	}
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			return fmt.Errorf("platform control: %w", err)
		}
		return fmt.Errorf("platform control: %w: %s", err, detail)
	}
	return nil
}
