package slate

import (
	"testing"
	"time"
)

func TestPlaybackWaitUntilKeepsOneClockAcrossRepeatedFiles(t *testing.T) {
	clockStart := time.Unix(100, 0)
	clockDTS := 10 * time.Second

	firstEnd := playbackWaitUntil(clockStart, clockDTS, 12*time.Second)
	secondEnd := playbackWaitUntil(clockStart, clockDTS, 14*time.Second)

	if got := firstEnd.Sub(clockStart); got != 2*time.Second {
		t.Fatalf("first cycle offset = %v, want 2s", got)
	}
	if got := secondEnd.Sub(firstEnd); got != 2*time.Second {
		t.Fatalf("second cycle spacing = %v, want 2s", got)
	}
}
