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
	// DelayedRange returns a decodable first range ending at the requested
	// delayed point, or the packets that follow a previously published point.
	// It lets a reader stay behind live continuously instead of draining one
	// finite snapshot and unintentionally catching up.
	DelayedRange(targetDelay, after time.Duration, initial bool) ([]media.Packet, time.Duration, error)
}

// PrefetchDelayedStore supplies an initial delayed range that starts on the
// keyframe before the requested point and extends a little ahead for player
// continuity. Subsequent ranges use DelayedStore normally.
type PrefetchDelayedStore interface {
	DelayedInitialRange(targetDelay, prefetch time.Duration) ([]media.Packet, time.Duration, error)
}

// LatestKeyframeStore returns only the current decodable GOP. It is used for
// seamless return-to-live without reading the entire delayed buffer.
type LatestKeyframeStore interface {
	Store
	LatestKeyframeSnapshot() ([]media.Packet, error)
}

type RetentionStore interface {
	SetMaxDuration(maxDuration time.Duration)
}

type Stats struct {
	Packets  int
	Duration time.Duration
	Bytes    int
}
