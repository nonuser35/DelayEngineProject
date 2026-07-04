package input

import "context"

// Reader receives encoded media packets from an input stream.
//
// Implementations must not decode, recode, store, or mutate audio/video payloads.
type Reader interface {
	Run(ctx context.Context) error
	Stats() Stats
}

type Stats struct {
	Connected           bool
	AudioPackets        uint64
	VideoPackets        uint64
	BytesRead           uint64
	VideoCodec          string
	AudioCodec          string
	Width               int
	Height              int
	FPS                 float64
	BitrateKbps         float64
	KeyframeInterval    string
	KeyframeIntervalSec float64
}
