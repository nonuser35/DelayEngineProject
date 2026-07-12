package control

import (
	"context"
	"fmt"
	"time"

	"delayengine/internal/api"
	"delayengine/internal/buffer"
	"delayengine/internal/delay"
	"delayengine/internal/input"
	inputrtmp "delayengine/internal/input/rtmp"
	"delayengine/internal/output"
)

const maxDelayDuration = 60 * time.Second

type Controller struct {
	delayState *delay.State
	buffer     buffer.Store
	input      input.Reader
	output     output.Publisher
}

func NewController(delayState *delay.State, packetBuffer buffer.Store, inputReader input.Reader, publisher output.Publisher) *Controller {
	return &Controller{
		delayState: delayState,
		buffer:     packetBuffer,
		input:      inputReader,
		output:     publisher,
	}
}

func (c *Controller) Status(ctx context.Context) (api.Status, error) {
	_ = ctx

	bufferStats := buffer.Stats{}
	if c.buffer != nil {
		bufferStats = c.buffer.Stats()
	}

	inputStats := input.Stats{}
	if c.input != nil {
		inputStats = c.input.Stats()
	}

	outputStats := output.Stats{}
	if c.output != nil {
		outputStats = c.output.Stats()
	}

	delayValue := time.Duration(0)
	delayEnabled := false
	if c.delayState != nil {
		delayEnabled = c.delayState.Enabled()
		if delayEnabled {
			delayValue = c.delayState.Delay()
		}
	}

	return api.Status{
		OK:           inputStats.Connected && outputStats.Connected,
		DelayEnabled: delayEnabled,
		Delay:        delayValue.String(),
		DelaySeconds: delayValue.Seconds(),
		Buffer: api.BufferStatus{
			Packets:  bufferStats.Packets,
			Duration: bufferStats.Duration.String(),
			Bytes:    bufferStats.Bytes,
		},
		Input: api.StreamStatus{
			Connected:           inputStats.Connected,
			Audio:               inputStats.AudioPackets,
			Video:               inputStats.VideoPackets,
			Bytes:               inputStats.BytesRead,
			VideoCodec:          inputStats.VideoCodec,
			AudioCodec:          inputStats.AudioCodec,
			Width:               inputStats.Width,
			Height:              inputStats.Height,
			FPS:                 inputStats.FPS,
			BitrateKbps:         inputStats.BitrateKbps,
			KeyframeInterval:    inputStats.KeyframeInterval,
			KeyframeIntervalSec: inputStats.KeyframeIntervalSec,
			KeyframeAgeMillis:   float64(inputStats.KeyframeAge) / float64(time.Millisecond),
			OutputQueue:         inputStats.OutputQueue,
			RealtimeState:       inputStats.RealtimeState,
			RealtimeDrops:       inputStats.RealtimeDrops,
		},
		Output: api.StreamStatus{
			Connected: outputStats.Connected,
			Audio:     outputStats.AudioPackets,
			Video:     outputStats.VideoPackets,
			Bytes:     outputStats.BytesPublished,
		},
		Note: "runtime delay target is applied without restarting the input, output, or buffer",
	}, nil
}

func (c *Controller) EnableDelay(ctx context.Context) error {
	_ = ctx
	if c.delayState != nil {
		c.delayState.Enable()
	}
	return nil
}

func (c *Controller) DisableDelay(ctx context.Context) error {
	reader, ok := c.input.(*inputrtmp.Reader)
	if !ok || reader == nil {
		if c.delayState != nil {
			c.delayState.SetDelay(0)
			c.delayState.Disable()
		}
		return nil
	}
	return reader.ForceRealtime(ctx)
}

func (c *Controller) SmoothDisableDelay(ctx context.Context) error {
	_ = ctx
	if c.delayState != nil {
		c.delayState.SetDelay(0)
		c.delayState.Enable()
	}
	return nil
}

func (c *Controller) ForceRealtime(ctx context.Context) error {
	reader, ok := c.input.(*inputrtmp.Reader)
	if !ok || reader == nil {
		if c.delayState != nil {
			c.delayState.SetDelay(0)
			c.delayState.Disable()
		}
		return nil
	}
	return reader.ForceRealtime(ctx)
}

func (c *Controller) ForceRealtimeReset(ctx context.Context) error {
	return c.ForceRealtimeResetPause(ctx, 0)
}

func (c *Controller) ForceRealtimeResetPause(ctx context.Context, pause time.Duration) error {
	reader, ok := c.input.(*inputrtmp.Reader)
	if !ok || reader == nil {
		if c.delayState != nil {
			c.delayState.SetDelay(0)
			c.delayState.Disable()
		}
		return nil
	}
	return reader.ForceRealtimeWithResetPause(ctx, pause)
}

func (c *Controller) SetDelay(ctx context.Context, delay time.Duration) error {
	_ = ctx
	if delay > maxDelayDuration {
		return fmt.Errorf("delay %s exceeds maximum supported delay %s", delay, maxDelayDuration)
	}
	if c.delayState != nil {
		c.delayState.SetDelay(delay)
	}
	return nil
}

func (c *Controller) ArmDelay(ctx context.Context, delayValue time.Duration, slatePath string, playFullSlate bool, shortSlate bool) error {
	if delayValue > maxDelayDuration {
		return fmt.Errorf("delay %s exceeds maximum supported delay %s", delayValue, maxDelayDuration)
	}
	reader, ok := c.input.(*inputrtmp.Reader)
	if !ok || reader == nil {
		return nil
	}
	return reader.ArmDelay(ctx, inputrtmp.ArmDelayRequest{Delay: delayValue, SlatePath: slatePath, PlayFullSlate: playFullSlate, ShortSlate: shortSlate})
}

func (c *Controller) ArmDelayFromBuffer(ctx context.Context, delayValue time.Duration) error {
	if delayValue > maxDelayDuration {
		return fmt.Errorf("delay %s exceeds maximum supported delay %s", delayValue, maxDelayDuration)
	}
	reader, ok := c.input.(*inputrtmp.Reader)
	if !ok || reader == nil {
		return nil
	}
	return reader.ArmDelayFromBuffer(ctx, delayValue)
}
