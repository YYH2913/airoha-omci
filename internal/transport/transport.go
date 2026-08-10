// SPDX-License-Identifier: Apache-2.0

package transport

import "context"

const MaxFrameSize = 1980

// Conn carries bare OMCI frames. Implementations must not add or remove an
// Ethernet header.
type Conn interface {
	ReadFrame(context.Context) ([]byte, error)
	WriteFrame(context.Context, []byte) error
	Close() error
}
