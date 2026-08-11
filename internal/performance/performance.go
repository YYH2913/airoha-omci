// SPDX-License-Identifier: Apache-2.0

// Package performance defines the platform-independent counter values used by
// G.988 performance monitoring managed entities.
package performance

type GEMPortCounters struct {
	ReceivedGEMFrames       uint64 `json:"received_gem_frames"`
	ReceivedPayloadBytes    uint64 `json:"received_payload_bytes"`
	TransmittedGEMFrames    uint64 `json:"transmitted_gem_frames"`
	TransmittedPayloadBytes uint64 `json:"transmitted_payload_bytes"`
}

type FECCounters struct {
	CorrectedBytes         uint64 `json:"corrected_bytes"`
	CorrectedCodeWords     uint64 `json:"corrected_codewords"`
	UncorrectableCodeWords uint64 `json:"uncorrectable_codewords"`
	TotalCodeWords         uint64 `json:"total_codewords"`
	FECSeconds             uint64 `json:"fec_seconds"`
}

type EthernetDirectionCounters struct {
	Frames          uint64
	Octets          uint64
	DropEvents      uint64
	BroadcastFrames uint64
	MulticastFrames uint64
	CRCErrors       uint64
	BufferOverflows uint64
	InternalErrors  uint64
	UndersizeFrames uint64
	Fragments       uint64
	Jabbers         uint64
	OversizeFrames  uint64
	SizeBuckets     [6]uint64
}

type EthernetCounters struct {
	Received    EthernetDirectionCounters
	Transmitted EthernetDirectionCounters
}

type Controller interface {
	GEMPortCounters(portID uint16) (GEMPortCounters, error)
}

type EthernetController interface {
	EthernetCounters(entityID uint16) (EthernetCounters, error)
}

type FECController interface {
	FECCounters(aniEntityID uint16) (FECCounters, error)
}
