package buffer

import (
	"time"

	"delayengine/pkg/media"
)

// Store keeps encoded packets in memory for delayed publication.
type Store interface {
	Add(packet media.Packet) error
	Snapshot() []media.Packet
	Len() int
	Duration() time.Duration
	Stats() Stats
	Clear()
}

type DelayedStore interface {
	Store
	DelayedSnapshot(targetDelay time.Duration) ([]media.Packet, time.Duration, error)
}

type Stats struct {
	Packets  int
	Duration time.Duration
	Bytes    int
}
