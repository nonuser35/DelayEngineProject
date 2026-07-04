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
