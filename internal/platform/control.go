// SPDX-License-Identifier: Apache-2.0

package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/xg2010g/airoha-omci/internal/optical"
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
	_, err := c.execute(controlRequest{Action: "synchronize-time", UnixTime: value.Unix()})
	return err
}

func (c ExecController) Reboot(condition uint8) error {
	_, err := c.execute(controlRequest{Action: "reboot", RebootCondition: condition})
	return err
}

func (c ExecController) OpticalLineSupervision() (optical.Diagnostics, error) {
	output, err := c.execute(controlRequest{Action: "optical-line-supervision"})
	if err != nil {
		return optical.Diagnostics{}, err
	}
	type response struct {
		Temperature      *uint16 `json:"temperature"`
		SupplyVoltage    *uint16 `json:"supply_voltage"`
		LaserBiasCurrent *uint16 `json:"laser_bias_current"`
		TransmitPower    *uint16 `json:"transmit_power"`
		ReceivePower     *uint16 `json:"receive_power"`
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	var value response
	if err := decoder.Decode(&value); err != nil {
		return optical.Diagnostics{}, fmt.Errorf("decode optical diagnostics: %w", err)
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return optical.Diagnostics{}, fmt.Errorf("decode optical diagnostics: trailing JSON value")
		}
		return optical.Diagnostics{}, fmt.Errorf("decode optical diagnostics: %w", err)
	}
	if value.Temperature == nil || value.SupplyVoltage == nil ||
		value.LaserBiasCurrent == nil || value.TransmitPower == nil ||
		value.ReceivePower == nil {
		return optical.Diagnostics{}, fmt.Errorf("decode optical diagnostics: required field is missing")
	}
	return (optical.Sample{
		Temperature:      *value.Temperature,
		SupplyVoltage:    *value.SupplyVoltage,
		LaserBiasCurrent: *value.LaserBiasCurrent,
		TransmitPower:    *value.TransmitPower,
		ReceivePower:     *value.ReceivePower,
	}).OMCI(), nil
}

func (c ExecController) execute(request controlRequest) ([]byte, error) {
	if c.Path == "" {
		return nil, fmt.Errorf("platform control helper is empty")
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = defaultApplyTimeout
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode platform control: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, c.Path)
	command.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err = command.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("platform control timed out after %s: %w", timeout, ctx.Err())
	}
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		if detail == "" {
			return nil, fmt.Errorf("platform control: %w", err)
		}
		return nil, fmt.Errorf("platform control: %w: %s", err, detail)
	}
	return stdout.Bytes(), nil
}
