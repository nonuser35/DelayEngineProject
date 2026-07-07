package buffer

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"delayengine/pkg/media"

	"github.com/bluenviron/gortmplib/pkg/message"
)

const defaultSegmentMaxBytes int64 = 64 * 1024 * 1024

type DiskOptions struct {
	Directory       string
	MaxDuration     time.Duration
	SegmentMaxBytes int64
}

type Disk struct {
	mu              sync.RWMutex
	dir             string
	maxDuration     time.Duration
	segmentMaxBytes int64
	entries         []diskEntry
	bytes           int
	currentSegment  int64
	currentFile     *os.File
	currentSize     int64
}

type diskEntry struct {
	Segment    int64
	Offset     int64
	Length     int64
	Timestamp  time.Duration
	PacketType media.PacketType
	IsKeyFrame bool
	Size       int
}

type diskRecord struct {
	Type       media.PacketType
	Codec      string
	PTS        time.Duration
	DTS        time.Duration
	IsKeyFrame bool
	ReceivedAt time.Time
	Audio      *message.Audio
	Video      *message.Video
}

func NewDisk(options DiskOptions) (*Disk, error) {
	if options.MaxDuration <= 0 {
		options.MaxDuration = time.Hour
	}
	if options.SegmentMaxBytes <= 0 {
		options.SegmentMaxBytes = defaultSegmentMaxBytes
	}
	if options.Directory == "" {
		return nil, fmt.Errorf("disk buffer directory is required")
	}
	if err := os.MkdirAll(options.Directory, 0755); err != nil {
		return nil, err
	}
	if err := clearDir(options.Directory); err != nil {
		return nil, err
	}

	store := &Disk{
		dir:             options.Directory,
		maxDuration:     options.MaxDuration,
		segmentMaxBytes: options.SegmentMaxBytes,
	}
	if err := store.rotateLocked(); err != nil {
		return nil, err
	}
	return store, nil
}

func (b *Disk) Add(packet media.Packet) error {
	if packet.ReceivedAt.IsZero() {
		packet.ReceivedAt = time.Now()
	}
	record, size, err := recordFromPacket(packet)
	if err != nil {
		return nil
	}

	var payload bytes.Buffer
	if err := gob.NewEncoder(&payload).Encode(record); err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.currentFile == nil || b.currentSize+int64(payload.Len()) > b.segmentMaxBytes {
		if err := b.rotateLocked(); err != nil {
			return err
		}
	}

	offset := b.currentSize
	written, err := b.currentFile.Write(payload.Bytes())
	if err != nil {
		return err
	}
	if written != payload.Len() {
		return fmt.Errorf("short write to disk buffer")
	}
	b.currentSize += int64(written)
	b.entries = append(b.entries, diskEntry{
		Segment:    b.currentSegment,
		Offset:     offset,
		Length:     int64(written),
		Timestamp:  packet.Timestamp(),
		PacketType: packet.Type,
		IsKeyFrame: packet.IsKeyFrame,
		Size:       size,
	})
	b.bytes += size
	b.trimLocked()
	return nil
}

func (b *Disk) Snapshot() []media.Packet {
	b.mu.RLock()
	entries := append([]diskEntry(nil), b.entries...)
	b.mu.RUnlock()

	packets, _ := b.readEntries(entries)
	return packets
}

func (b *Disk) DelayedSnapshot(targetDelay time.Duration) ([]media.Packet, time.Duration, error) {
	if targetDelay <= 0 {
		return nil, 0, nil
	}

	b.mu.RLock()
	if len(b.entries) == 0 {
		b.mu.RUnlock()
		return nil, 0, nil
	}
	latest := b.entries[len(b.entries)-1].Timestamp
	target := latest - targetDelay
	if target < 0 {
		target = 0
	}

	start := 0
	for i, entry := range b.entries {
		if entry.Timestamp >= target {
			start = i
			break
		}
	}
	for i := start; i >= 0; i-- {
		entry := b.entries[i]
		if entry.PacketType == media.PacketTypeVideo && entry.IsKeyFrame {
			start = i
			break
		}
	}

	entries := append([]diskEntry(nil), b.entries[start:]...)
	snapshotDelay := latest - b.entries[start].Timestamp
	b.mu.RUnlock()

	packets, err := b.readEntries(entries)
	return packets, snapshotDelay, err
}

func (b *Disk) Len() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.entries)
}

func (b *Disk) Duration() time.Duration {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.durationLocked()
}

func (b *Disk) Stats() Stats {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return Stats{
		Packets:  len(b.entries),
		Duration: b.durationLocked(),
		Bytes:    b.bytes,
	}
}

func (b *Disk) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.currentFile != nil {
		_ = b.currentFile.Close()
		b.currentFile = nil
	}
	_ = clearDir(b.dir)
	b.entries = nil
	b.bytes = 0
	b.currentSegment = 0
	b.currentSize = 0
	_ = b.rotateLocked()
}

func (b *Disk) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.currentFile == nil {
		return nil
	}
	err := b.currentFile.Close()
	b.currentFile = nil
	return err
}

func (b *Disk) rotateLocked() error {
	if b.currentFile != nil {
		if err := b.currentFile.Close(); err != nil {
			return err
		}
	}
	b.currentSegment++
	path := b.segmentPath(b.currentSegment)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	b.currentFile = file
	b.currentSize = 0
	return nil
}

func (b *Disk) trimLocked() {
	if len(b.entries) < 2 || b.durationLocked() <= b.maxDuration {
		return
	}

	oldestSegment := b.entries[0].Segment
	newest := b.entries[len(b.entries)-1].Timestamp
	cutoff := 0
	for cutoff < len(b.entries)-1 && newest-b.entries[cutoff].Timestamp > b.maxDuration {
		b.bytes -= b.entries[cutoff].Size
		cutoff++
	}
	if cutoff == 0 {
		return
	}

	if cutoff > len(b.entries)/2 {
		kept := append([]diskEntry(nil), b.entries[cutoff:]...)
		b.entries = kept
	} else {
		b.entries = b.entries[cutoff:]
	}

	keptOldestSegment := b.entries[0].Segment
	for segment := oldestSegment; segment < keptOldestSegment; segment++ {
		_ = os.Remove(b.segmentPath(segment))
	}
}

func (b *Disk) durationLocked() time.Duration {
	if len(b.entries) < 2 {
		return 0
	}
	oldest := b.entries[0].Timestamp
	newest := b.entries[len(b.entries)-1].Timestamp
	if newest <= oldest {
		return 0
	}
	return newest - oldest
}

func (b *Disk) readEntries(entries []diskEntry) ([]media.Packet, error) {
	packets := make([]media.Packet, 0, len(entries))
	segmentCache := map[int64][]byte{}
	for _, entry := range entries {
		data, ok := segmentCache[entry.Segment]
		if !ok {
			var err error
			data, err = os.ReadFile(b.segmentPath(entry.Segment))
			if err != nil {
				return packets, err
			}
			segmentCache[entry.Segment] = data
		}
		if entry.Offset < 0 || entry.Length < 0 || entry.Offset+entry.Length > int64(len(data)) {
			return packets, fmt.Errorf("invalid disk buffer entry")
		}
		var record diskRecord
		if err := gob.NewDecoder(bytes.NewReader(data[entry.Offset : entry.Offset+entry.Length])).Decode(&record); err != nil {
			return packets, err
		}
		packet, ok := packetFromRecord(record)
		if ok {
			packets = append(packets, packet)
		}
	}
	return packets, nil
}

func (b *Disk) segmentPath(segment int64) string {
	return filepath.Join(b.dir, fmt.Sprintf("segment-%06d.deb", segment))
}

func recordFromPacket(packet media.Packet) (diskRecord, int, error) {
	record := diskRecord{
		Type:       packet.Type,
		Codec:      packet.Codec,
		PTS:        packet.PTS,
		DTS:        packet.DTS,
		IsKeyFrame: packet.IsKeyFrame,
		ReceivedAt: packet.ReceivedAt,
	}

	switch msg := packet.RTMPMessage.(type) {
	case *message.Audio:
		clone := *msg
		clone.AU = append([]byte(nil), msg.AU...)
		record.Audio = &clone
		return record, len(clone.AU), nil
	case *message.Video:
		clone := *msg
		clone.AU = append([]byte(nil), msg.AU...)
		record.Video = &clone
		return record, len(clone.AU), nil
	default:
		return diskRecord{}, 0, fmt.Errorf("unsupported packet for disk buffer")
	}
}

func packetFromRecord(record diskRecord) (media.Packet, bool) {
	packet := media.Packet{
		Type:       record.Type,
		Codec:      record.Codec,
		PTS:        record.PTS,
		DTS:        record.DTS,
		IsKeyFrame: record.IsKeyFrame,
		ReceivedAt: record.ReceivedAt,
	}

	switch {
	case record.Audio != nil:
		clone := *record.Audio
		clone.AU = append([]byte(nil), record.Audio.AU...)
		packet.Data = clone.AU
		packet.RTMPMessage = &clone
	case record.Video != nil:
		clone := *record.Video
		clone.AU = append([]byte(nil), record.Video.AU...)
		packet.Data = clone.AU
		packet.RTMPMessage = &clone
	default:
		return media.Packet{}, false
	}
	return packet, true
}

func clearDir(path string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(path, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}
