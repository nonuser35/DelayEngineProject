package rtmp

import (
	"context"
	"testing"
	"time"

	"delayengine/internal/delay"

	"github.com/bluenviron/gortmplib/pkg/message"
)

func TestIncreaseEffectiveDelayIncreasesGradually(t *testing.T) {
	current := time.Duration(0)
	target := 30 * time.Second

	next := increaseEffectiveDelay(current, target, 40*time.Millisecond)
	if next <= 0 {
		t.Fatalf("next = %v, want positive", next)
	}
	if next >= target {
		t.Fatalf("next = %v, want gradual increase below target", next)
	}
}

func TestDecreaseEffectiveDelayDecreasesGradually(t *testing.T) {
	current := 30 * time.Second
	target := time.Duration(0)

	next := decreaseEffectiveDelay(current, target, 40*time.Millisecond)
	if next >= current {
		t.Fatalf("next = %v, want below current", next)
	}
	if next <= target {
		t.Fatalf("next = %v, want gradual decrease above target", next)
	}
}

func TestDecreaseEffectiveDelayDoesNotUndershoot(t *testing.T) {
	current := 2 * time.Millisecond
	target := time.Duration(0)

	next := decreaseEffectiveDelay(current, target, time.Second)
	if next != target {
		t.Fatalf("next = %v, want %v", next, target)
	}
}

func TestSyncDownStepIsConservative(t *testing.T) {
	step := syncDownStep(200 * time.Millisecond)
	if step > 12*time.Millisecond {
		t.Fatalf("step = %v, want at most 12ms", step)
	}
}

func TestIncreaseEffectiveDelayDoesNotOvershoot(t *testing.T) {
	current := 995 * time.Millisecond
	target := time.Second

	next := increaseEffectiveDelay(current, target, time.Second)
	if next != target {
		t.Fatalf("next = %v, want %v", next, target)
	}
}

func TestIsResumePointRequiresVideoKeyFrameWhenVideoExists(t *testing.T) {
	if isResumePoint(&message.Audio{}, true) {
		t.Fatalf("audio must not resume while video exists")
	}
	if isResumePoint(&message.Video{Type: message.VideoTypeAU, IsKeyFrame: false}, true) {
		t.Fatalf("non-keyframe video must not resume")
	}
	if !isResumePoint(&message.Video{Type: message.VideoTypeAU, IsKeyFrame: true}, true) {
		t.Fatalf("keyframe video should resume")
	}
}

func TestIsResumePointAllowsAudioOnly(t *testing.T) {
	if !isResumePoint(&message.Audio{}, false) {
		t.Fatalf("audio should resume when stream has no video")
	}
}

func TestMediaPayloadDTSIgnoresConfigPackets(t *testing.T) {
	_, ok := mediaPayloadDTS(&message.Audio{
		Codec:   message.CodecMPEG4Audio,
		AACType: message.AudioAACTypeConfig,
	})
	if ok {
		t.Fatalf("AAC config must not arm publish clock")
	}

	_, ok = mediaPayloadDTS(&message.Video{
		Codec: message.CodecH264,
		Type:  message.VideoTypeConfig,
	})
	if ok {
		t.Fatalf("video config must not arm publish clock")
	}
}

func TestMediaPayloadDTSAcceptsMediaPackets(t *testing.T) {
	_, ok := mediaPayloadDTS(&message.Audio{
		Codec:   message.CodecMPEG4Audio,
		AACType: message.AudioAACTypeAU,
		DTS:     time.Second,
	})
	if !ok {
		t.Fatalf("AAC AU should arm publish clock")
	}

	_, ok = mediaPayloadDTS(&message.Video{
		Codec: message.CodecH264,
		Type:  message.VideoTypeAU,
		DTS:   time.Second,
	})
	if !ok {
		t.Fatalf("video AU should arm publish clock")
	}
}

func TestDelaySyncMode(t *testing.T) {
	if got := delaySyncMode(time.Second, time.Second); got != "stable" {
		t.Fatalf("got %q, want stable", got)
	}
	if got := delaySyncMode(time.Second, 2*time.Second); got != "increasing" {
		t.Fatalf("got %q, want increasing", got)
	}
	if got := delaySyncMode(2*time.Second, time.Second); got != "catching_up" {
		t.Fatalf("got %q, want catching_up", got)
	}
}

func TestSleepUntilOrDelayChangeWakesOnVersionChange(t *testing.T) {
	state := delay.NewState(true, 30*time.Second)
	reader := NewReader(ReaderConfig{DelayState: state})

	done := make(chan bool, 1)
	go func() {
		changed, err := reader.sleepUntilOrDelayChange(context.Background(), time.Now().Add(10*time.Second), state.Version())
		if err != nil {
			t.Errorf("sleepUntilOrDelayChange() error = %v", err)
		}
		done <- changed
	}()

	time.Sleep(50 * time.Millisecond)
	state.Disable()

	select {
	case changed := <-done:
		if !changed {
			t.Fatalf("changed = false, want true")
		}
	case <-time.After(time.Second):
		t.Fatalf("sleep did not wake after delay state changed")
	}
}

func TestArmDelayRejectsDelayLargerThanBufferWindow(t *testing.T) {
	reader := NewReader(ReaderConfig{})

	err := reader.ArmDelay(context.Background(), ArmDelayRequest{
		Delay:     61 * time.Second,
		SlatePath: "loading.flv",
	})
	if err == nil {
		t.Fatalf("ArmDelay() error = nil, want max delay error")
	}
}

func TestPrepareMessageForPublishZerosInitializationBeforeBase(t *testing.T) {
	original := &message.Audio{
		Codec:   message.CodecMPEG4Audio,
		AACType: message.AudioAACTypeConfig,
		DTS:     10 * time.Second,
	}

	prepared, _, ok := prepareMessageForPublish(original, 0, 0, false)
	if ok {
		t.Fatalf("ok = true, want false for initialization packet")
	}
	preparedAudio := prepared.(*message.Audio)
	if preparedAudio.DTS != 0 {
		t.Fatalf("prepared DTS = %v, want 0", preparedAudio.DTS)
	}
	if original.DTS != 10*time.Second {
		t.Fatalf("original DTS changed to %v", original.DTS)
	}
}

func TestPrepareMessageForPublishNormalizesTimestamp(t *testing.T) {
	original := &message.Audio{
		Codec: message.CodecMPEG4Audio,
		DTS:   10 * time.Second,
		AU:    []byte{1, 2, 3},
	}

	prepared, dts, ok := prepareMessageForPublish(original, 10*time.Second, 0, true)
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	if dts != 0 {
		t.Fatalf("dts = %v, want 0", dts)
	}
	preparedAudio := prepared.(*message.Audio)
	if preparedAudio.DTS != 0 {
		t.Fatalf("prepared DTS = %v, want 0", preparedAudio.DTS)
	}
	if original.DTS != 10*time.Second {
		t.Fatalf("original DTS changed to %v", original.DTS)
	}
	if len(preparedAudio.AU) != 3 || preparedAudio.AU[0] != 1 {
		t.Fatalf("payload was not preserved")
	}
}

func TestNormalizeOutputDTSPreservesDistance(t *testing.T) {
	got := normalizeOutputDTS(12*time.Second, 10*time.Second, 0)
	if got != 2*time.Second {
		t.Fatalf("got %v, want 2s", got)
	}
}

func TestReadyToResumeAfterDelayDecreaseWaitsForSmallRealtimeQueue(t *testing.T) {
	if readyToResumeAfterDelayDecrease(0, 33) {
		t.Fatalf("zero delay must not resume with a large queued backlog")
	}
	if !readyToResumeAfterDelayDecrease(0, 32) {
		t.Fatalf("zero delay should resume once the queued backlog is small")
	}
}

func TestReadyToResumeAfterDelayDecreaseAllowsMoreQueueForDelayedTarget(t *testing.T) {
	if readyToResumeAfterDelayDecrease(15*time.Second, 257) {
		t.Fatalf("delayed target must not resume with too much queued backlog")
	}
	if !readyToResumeAfterDelayDecrease(15*time.Second, 256) {
		t.Fatalf("delayed target should resume once backlog is bounded")
	}
}
