// SPDX-License-Identifier: Apache-2.0

package transport

import "context"

const (
	// MaxFrameSize is the bare OMCI frame limit used by the GPON packet
	// transport and by the OMCI codec.
	MaxFrameSize = 1980
	// MaxDeviceContentSize leaves four bytes in the 1980-byte XGS wire frame
	// for the trusted driver's OMCI MIC.
	MaxDeviceContentSize = MaxFrameSize - 4
)

type Frame struct {
	Contents           []byte
	MICVerified        bool
	InstanceGeneration uint64
	SessionGeneration  uint64
}

type Capabilities struct {
	VerifiedDownstreamMIC bool
	SignedUpstreamMIC     bool
}

func (c Capabilities) SecureOMCC() bool {
	return c.VerifiedDownstreamMIC && c.SignedUpstreamMIC
}

// Conn carries bare OMCI frames. Implementations must not add an Ethernet
// header. MICVerified is trusted only when Capabilities reports secure OMCC.
type Conn interface {
	ReadFrame(context.Context) (Frame, error)
	WriteFrame(context.Context, []byte) error
	Capabilities() Capabilities
	Close() error
}
