// SPDX-License-Identifier: Apache-2.0

package optical

import "math"

// Sample is the SFF-8472 compatible EN7572 diagnostics block. Temperature is
// signed 1/256 C, voltage is 100 uV, bias is 2 uA and optical powers are 0.1 uW.
type Sample struct {
	Temperature      uint16 `json:"temperature"`
	SupplyVoltage    uint16 `json:"supply_voltage"`
	LaserBiasCurrent uint16 `json:"laser_bias_current"`
	TransmitPower    uint16 `json:"transmit_power"`
	ReceivePower     uint16 `json:"receive_power"`
}

// Diagnostics contains the five G.988 optical-line-supervision result values.
type Diagnostics struct {
	PowerFeedVoltage     uint16
	ReceivedOpticalPower uint16
	MeanOpticalLaunch    uint16
	LaserBiasCurrent     uint16
	Temperature          uint16
}

func (sample Sample) OMCI() Diagnostics {
	return Diagnostics{
		PowerFeedVoltage:     uint16((uint32(sample.SupplyVoltage) + 100) / 200),
		ReceivedOpticalPower: opticalPower(sample.ReceivePower),
		MeanOpticalLaunch:    opticalPower(sample.TransmitPower),
		LaserBiasCurrent:     sample.LaserBiasCurrent,
		Temperature:          sample.Temperature,
	}
}

// G.988 encodes optical power as signed dBu with 0.002 dB resolution. The
// EN7572 uses unsigned 0.1 uW units, so zero is saturated at the lowest value.
func opticalPower(raw uint16) uint16 {
	if raw == 0 {
		return uint16(0x8000)
	}
	value := math.Round(5000 * (math.Log10(float64(raw)) - 1))
	if value < math.MinInt16 {
		value = math.MinInt16
	} else if value > math.MaxInt16 {
		value = math.MaxInt16
	}
	return uint16(int16(value))
}
