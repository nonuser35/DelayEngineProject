package buffer

import (
	"testing"
	"time"

	"delayengine/pkg/media"
)

func TestCircularKeepsConfiguredDuration(t *testing.T) {
	store := NewCircular(60 * time.Second)

	for i := 0; i <= 70; i++ {
		err := store.Add(media.Packet{
			Type: media.PacketTypeVideo,
			PTS:  time.Duration(i) * time.Second,
			DTS:  time.Duration(i) * time.Second,
			Data: []byte{byte(i)},
		})
		if err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}

	stats := store.Stats()
	if stats.Duration > 60*time.Second {
		t.Fatalf("Duration() = %v, want <= 60s", stats.Duration)
	}
	if stats.Packets != 61 {
		t.Fatalf("Packets = %d, want 61", stats.Packets)
	}

	snapshot := store.Snapshot()
	if len(snapshot) != 61 {
		t.Fatalf("Snapshot length = %d, want 61", len(snapshot))
	}
	if snapshot[0].DTS != 10*time.Second {
		t.Fatalf("oldest DTS = %v, want 10s", snapshot[0].DTS)
	}
}

func TestCircularSnapshotClonesPackets(t *testing.T) {
	store := NewCircular(60 * time.Second)
	err := store.Add(media.Packet{
		Type:  media.PacketTypeAudio,
		PTS:   time.Second,
		DTS:   time.Second,
		Data:  []byte{1, 2, 3},
		Parts: [][]byte{{4, 5, 6}},
	})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	snapshot := store.Snapshot()
	snapshot[0].Data[0] = 9
	snapshot[0].Parts[0][0] = 9

	again := store.Snapshot()
	if again[0].Data[0] != 1 {
		t.Fatalf("Data was not cloned")
	}
	if again[0].Parts[0][0] != 4 {
		t.Fatalf("Parts were not cloned")
	}
}
