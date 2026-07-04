package control

import (
	"context"
	"testing"
	"time"

	"delayengine/internal/delay"
)

func TestSetDelayRejectsDelayLargerThanBufferWindow(t *testing.T) {
	controller := NewController(delay.NewState(false, 0), nil, nil, nil)

	err := controller.SetDelay(context.Background(), 61*time.Second)
	if err == nil {
		t.Fatalf("SetDelay() error = nil, want max delay error")
	}
}

func TestArmDelayRejectsDelayLargerThanBufferWindow(t *testing.T) {
	controller := NewController(delay.NewState(false, 0), nil, nil, nil)

	err := controller.ArmDelay(context.Background(), 61*time.Second, "loading.flv", false)
	if err == nil {
		t.Fatalf("ArmDelay() error = nil, want max delay error")
	}
}
