package buffer

import (
	"testing"
	"time"

	"delayengine/pkg/media"

	"github.com/bluenviron/gortmplib/pkg/message"
)

func TestDiskDelayedSnapshotStartsOnKeyframe(t *testing.T) {
	store, err := NewDisk(DiskOptions{
		Directory:       t.TempDir(),
		MaxDuration:     10 * time.Second,
		SegmentMaxBytes: 1024,
	})
	if err != nil {
		t.Fatalf("NewDisk() error = %v", err)
	}
	defer store.Close()

	for i := 0; i <= 9; i++ {
		keyframe := i == 0 || i == 4 || i == 8
		err := store.Add(media.Packet{
			Type:       media.PacketTypeVideo,
			Codec:      "H264",
			PTS:        time.Duration(i) * time.Second,
			DTS:        time.Duration(i) * time.Second,
			IsKeyFrame: keyframe,
			Data:       []byte{byte(i)},
			RTMPMessage: &message.Video{
				ChunkStreamID:   6,
				DTS:             time.Duration(i) * time.Second,
				MessageStreamID: 1,
				Codec:           message.CodecH264,
				Type:            message.VideoTypeAU,
				IsKeyFrame:      keyframe,
				AU:              []byte{byte(i)},
			},
		})
		if err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}

	packets, snapshotDelay, err := store.DelayedSnapshot(3 * time.Second)
	if err != nil {
		t.Fatalf("DelayedSnapshot() error = %v", err)
	}
	if len(packets) == 0 {
		t.Fatalf("DelayedSnapshot() returned no packets")
	}
	if packets[0].Timestamp() != 4*time.Second {
		t.Fatalf("first packet DTS = %v, want 4s keyframe", packets[0].Timestamp())
	}
	if snapshotDelay != 5*time.Second {
		t.Fatalf("snapshotDelay = %v, want 5s", snapshotDelay)
	}
}

func TestDiskDelayedRangeStopsAtDelayedCursor(t *testing.T) {
	store, err := NewDisk(DiskOptions{Directory: t.TempDir(), MaxDuration: time.Minute})
	if err != nil {
		t.Fatalf("NewDisk() error = %v", err)
	}
	defer store.Close()
	for i := 0; i <= 9; i++ {
		keyframe := i == 0 || i == 4 || i == 8
		if err := store.Add(media.Packet{
			Type: media.PacketTypeVideo, DTS: time.Duration(i) * time.Second, IsKeyFrame: keyframe,
			RTMPMessage: &message.Video{Type: message.VideoTypeAU, DTS: time.Duration(i) * time.Second, IsKeyFrame: keyframe},
		}); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}
	packets, _, err := store.DelayedRange(3*time.Second, 0, true)
	if err != nil {
		t.Fatalf("DelayedRange() error = %v", err)
	}
	if len(packets) != 3 || packets[0].Timestamp() != 4*time.Second || packets[2].Timestamp() != 6*time.Second {
		t.Fatalf("initial delayed range = %#v, want 4s through 6s", packets)
	}
}

func TestDiskDelayedInitialRangeKeepsTargetKeyframeAndPrefetch(t *testing.T) {
	store, err := NewDisk(DiskOptions{Directory: t.TempDir(), MaxDuration: time.Minute})
	if err != nil {
		t.Fatalf("NewDisk() error = %v", err)
	}
	defer store.Close()
	for i := 0; i <= 12; i++ {
		keyframe := i == 0 || i == 4 || i == 8 || i == 12
		if err := store.Add(media.Packet{
			Type: media.PacketTypeVideo, DTS: time.Duration(i) * time.Second, IsKeyFrame: keyframe,
			RTMPMessage: &message.Video{Type: message.VideoTypeAU, DTS: time.Duration(i) * time.Second, IsKeyFrame: keyframe},
		}); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}
	packets, _, err := store.DelayedInitialRange(5*time.Second, 3*time.Second)
	if err != nil {
		t.Fatalf("DelayedInitialRange() error = %v", err)
	}
	if len(packets) != 7 || packets[0].Timestamp() != 4*time.Second || packets[len(packets)-1].Timestamp() != 10*time.Second {
		t.Fatalf("initial prefetch range = %#v, want 4s through 10s", packets)
	}
}

func TestDiskTrimsOldSegments(t *testing.T) {
	store, err := NewDisk(DiskOptions{
		Directory:       t.TempDir(),
		MaxDuration:     3 * time.Second,
		SegmentMaxBytes: 256,
	})
	if err != nil {
		t.Fatalf("NewDisk() error = %v", err)
	}
	defer store.Close()

	for i := 0; i <= 10; i++ {
		err := store.Add(media.Packet{
			Type:       media.PacketTypeVideo,
			Codec:      "H264",
			PTS:        time.Duration(i) * time.Second,
			DTS:        time.Duration(i) * time.Second,
			IsKeyFrame: true,
			Data:       []byte{byte(i)},
			RTMPMessage: &message.Video{
				ChunkStreamID:   6,
				DTS:             time.Duration(i) * time.Second,
				MessageStreamID: 1,
				Codec:           message.CodecH264,
				Type:            message.VideoTypeAU,
				IsKeyFrame:      true,
				AU:              []byte{byte(i)},
			},
		})
		if err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}

	if duration := store.Duration(); duration > 3*time.Second {
		t.Fatalf("Duration() = %v, want <= 3s", duration)
	}
}

func TestDiskLatestKeyframeSnapshotReadsOnlyCurrentGOP(t *testing.T) {
	store, err := NewDisk(DiskOptions{Directory: t.TempDir(), MaxDuration: time.Minute})
	if err != nil {
		t.Fatalf("NewDisk() error = %v", err)
	}
	defer store.Close()

	for i := 0; i < 10; i++ {
		keyframe := i == 2 || i == 8
		if err := store.Add(media.Packet{
			Type: media.PacketTypeVideo, DTS: time.Duration(i) * time.Second, IsKeyFrame: keyframe,
			RTMPMessage: &message.Video{Type: message.VideoTypeAU, DTS: time.Duration(i) * time.Second, IsKeyFrame: keyframe},
		}); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}

	packets, err := store.LatestKeyframeSnapshot()
	if err != nil {
		t.Fatalf("LatestKeyframeSnapshot() error = %v", err)
	}
	if len(packets) != 2 || packets[0].Timestamp() != 8*time.Second {
		t.Fatalf("snapshot = %#v, want packets starting at the 8s keyframe", packets)
	}
}
