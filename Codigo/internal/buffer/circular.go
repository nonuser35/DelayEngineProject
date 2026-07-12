package buffer

import (
	"sync"
	"time"

	"delayengine/pkg/media"
)

const defaultInitialCapacity = 4096

type Circular struct {
	mu          sync.RWMutex
	maxDuration time.Duration
	packets     []media.Packet
	head        int
	len         int
	bytes       int
}

func NewCircular(maxDuration time.Duration) *Circular {
	if maxDuration <= 0 {
		maxDuration = 60 * time.Second
	}

	return &Circular{
		maxDuration: maxDuration,
		packets:     make([]media.Packet, defaultInitialCapacity),
	}
}

func (b *Circular) Add(packet media.Packet) error {
	if packet.ReceivedAt.IsZero() {
		packet.ReceivedAt = time.Now()
	}
	packet = packet.Clone()

	b.mu.Lock()
	defer b.mu.Unlock()

	b.push(packet)
	b.trimLocked()
	return nil
}

func (b *Circular) Snapshot() []media.Packet {
	b.mu.RLock()
	defer b.mu.RUnlock()

	ret := make([]media.Packet, b.len)
	for i := 0; i < b.len; i++ {
		ret[i] = b.packets[b.index(i)].Clone()
	}
	return ret
}

func (b *Circular) Len() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.len
}

func (b *Circular) Duration() time.Duration {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.durationLocked()
}

func (b *Circular) SetMaxDuration(maxDuration time.Duration) {
	if maxDuration <= 0 {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.maxDuration == maxDuration {
		return
	}
	b.maxDuration = maxDuration
	b.trimLocked()
}

func (b *Circular) Stats() Stats {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return Stats{
		Packets:  b.len,
		Duration: b.durationLocked(),
		Bytes:    b.bytes,
	}
}

func (b *Circular) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()

	for i := range b.packets {
		b.packets[i] = media.Packet{}
	}
	b.head = 0
	b.len = 0
	b.bytes = 0
}

func (b *Circular) push(packet media.Packet) {
	if b.len == len(b.packets) {
		b.grow()
	}

	pos := b.index(b.len)
	b.packets[pos] = packet
	b.len++
	b.bytes += packet.Size()
}

func (b *Circular) grow() {
	next := make([]media.Packet, len(b.packets)*2)
	for i := 0; i < b.len; i++ {
		next[i] = b.packets[b.index(i)]
	}
	b.packets = next
	b.head = 0
}

func (b *Circular) trimLocked() {
	for b.len > 1 && b.durationLocked() > b.maxDuration {
		oldest := b.packets[b.head]
		b.bytes -= oldest.Size()
		b.packets[b.head] = media.Packet{}
		b.head = (b.head + 1) % len(b.packets)
		b.len--
	}
}

func (b *Circular) durationLocked() time.Duration {
	if b.len < 2 {
		return 0
	}

	oldest := b.packets[b.head].Timestamp()
	newest := b.packets[b.index(b.len-1)].Timestamp()
	if newest <= oldest {
		return 0
	}
	return newest - oldest
}

func (b *Circular) index(offset int) int {
	return (b.head + offset) % len(b.packets)
}
