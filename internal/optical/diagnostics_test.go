// SPDX-License-Identifier: Apache-2.0

package optical

import "testing"

func TestSampleOMCIUnits(t *testing.T) {
	sample := Sample{
		Temperature:      0xf600, // -10 C
		SupplyVoltage:    33000,  // 3.3 V
		LaserBiasCurrent: 2500,   // 5 mA
		TransmitPower:    10000,  // 0 dBm / 30 dBu
		ReceivePower:     10,     // -30 dBm / 0 dBu
	}
	got := sample.OMCI()
	if got.PowerFeedVoltage != 165 || got.ReceivedOpticalPower != 0 ||
		got.MeanOpticalLaunch != 15000 || got.LaserBiasCurrent != 2500 ||
		got.Temperature != 0xf600 {
		t.Fatalf("OMCI diagnostics = %#v", got)
	}
}

func TestOpticalPowerRoundsAndSaturates(t *testing.T) {
	tests := []struct {
		raw  uint16
		want int16
	}{
		{0, -32768},
		{1, -5000},
		{10, 0},
		{100, 5000},
		{10000, 15000},
	}
	for _, test := range tests {
		if got := int16(opticalPower(test.raw)); got != test.want {
			t.Errorf("opticalPower(%d) = %d, want %d", test.raw, got, test.want)
		}
	}
}
