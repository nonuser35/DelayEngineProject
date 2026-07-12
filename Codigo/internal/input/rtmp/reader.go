package rtmp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"delayengine/internal/buffer"
	"delayengine/internal/delay"
	"delayengine/internal/input"
	"delayengine/internal/netutil"
	"delayengine/internal/output"
	"delayengine/internal/slate"
	"delayengine/pkg/media"

	"github.com/bluenviron/gortmplib"
	"github.com/bluenviron/gortmplib/pkg/codecs"
	"github.com/bluenviron/gortmplib/pkg/message"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h265"
)

const (
	maxDelayDuration   = 60 * time.Second
	activeBufferWindow = 2 * time.Minute
	publishQueueSize   = 16384
	// Keep no more than roughly one second of realtime media pending. If the
	// publisher cannot keep up, resync at a keyframe instead of accumulating
	// latency for the viewer.
	realtimeQueueLimit = 120
	// Network delivery is naturally bursty. A short, stable queue below this
	// point is normal pacing headroom, not a viewer-visible recovery state.
	realtimeQueueHealthyLimit = 96
	// Direct-buffer output is supplied continuously. Keeping this queue short
	// prevents a slow downstream relay from turning queued media into a second,
	// growing delay that makes the player rebuffer.
	directDelayQueueLimit = 600
	// Queue a few seconds ahead of the delayed cursor at a transition. Those
	// packets are still paced by their media timestamps, so this does not add
	// viewer delay; it gives the downstream RTMP/HLS player enough continuity
	// to avoid a short rebuffer immediately after the slate.
	delayedCursorPrefetch = 3 * time.Second
	commandTimeout        = 10 * time.Second
	slateVisualLead       = time.Second
)

type ArmDelayRequest struct {
	Delay         time.Duration
	SlatePath     string
	PlayFullSlate bool
	ShortSlate    bool
}

type armDelayCommand struct {
	request ArmDelayRequest
	done    chan error
}

type bufferDelayCommand struct {
	delay time.Duration
	done  chan error
}

type syncLiveCommand struct {
	resetOutput bool
	resetPause  time.Duration
	done        chan error
}

type ReaderConfig struct {
	URL         string
	ReadTimeout time.Duration
	Logger      *slog.Logger
	Buffer      buffer.Store
	Publisher   output.Publisher
	DelayState  *delay.State
}

type Reader struct {
	url           string
	readTimeout   time.Duration
	logger        *slog.Logger
	buffer        buffer.Store
	publisher     output.Publisher
	delayState    *delay.State
	publishCh     chan message.Message
	armDelayCh    chan armDelayCommand
	bufferDelayCh chan bufferDelayCommand
	syncLiveCh    chan syncLiveCommand
	initMu        sync.RWMutex
	initMessages  []message.Message
	publishMu     sync.Mutex

	connected          atomic.Bool
	audioPackets       atomic.Uint64
	videoPackets       atomic.Uint64
	bytesRead          atomic.Uint64
	connectedAt        atomic.Int64
	videoWidth         atomic.Int64
	videoHeight        atomic.Int64
	videoCodec         atomic.Value
	audioCodec         atomic.Value
	lastKeyframeDTS    atomic.Int64
	lastKeyframeAt     atomic.Int64
	keyframeInterval   atomic.Int64
	effectiveDelayNS   atomic.Int64
	bufferWindowNS     atomic.Int64
	realtimeResync     atomic.Bool
	realtimeDrops      atomic.Uint64
	realtimeSeedLeft   atomic.Int64
	realtimeSeedActive atomic.Bool
	slateActive        atomic.Bool
	bufferDelayActive  atomic.Bool
	bufferDelayCursor  atomic.Int64
	firstAudio         atomic.Bool
	firstVideo         atomic.Bool
}

func NewReader(cfg ReaderConfig) *Reader {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Reader{
		url:           cfg.URL,
		readTimeout:   cfg.ReadTimeout,
		logger:        logger,
		buffer:        cfg.Buffer,
		publisher:     cfg.Publisher,
		delayState:    cfg.DelayState,
		publishCh:     make(chan message.Message, publishQueueSize),
		armDelayCh:    make(chan armDelayCommand),
		bufferDelayCh: make(chan bufferDelayCommand),
		syncLiveCh:    make(chan syncLiveCommand, 1),
	}
}

func (r *Reader) Run(ctx context.Context) error {
	if r.publisher != nil {
		go r.publishLoop(ctx)
	}

	statsDone := make(chan struct{})
	defer close(statsDone)
	go r.logStats(statsDone)

	for {
		err := r.runOnce(ctx)
		if ctx.Err() != nil {
			return nil
		}
		r.resetManualDelayAfterInputEnded()
		if err != nil {
			r.logger.Warn("RTMP input unavailable; retrying", "error", err, "retry_in", "1s", "status", "waiting")
		} else {
			r.logger.Warn("RTMP input disconnected; retrying", "retry_in", "1s", "status", "waiting")
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
	}
}

func (r *Reader) resetManualDelayAfterInputEnded() {
	if r.delayState == nil {
		return
	}

	enabled := r.delayState.Enabled()
	delayValue := r.delayState.Delay()
	effectiveDelay := time.Duration(r.effectiveDelayNS.Load())
	shouldResetDelay := enabled || delayValue != 0 || effectiveDelay != 0
	shouldClearBuffer := false
	if r.buffer != nil {
		shouldClearBuffer = r.buffer.Len() > 0
	}
	if !shouldResetDelay && !shouldClearBuffer {
		return
	}

	if shouldResetDelay {
		r.delayState.SetDelay(0)
		r.delayState.Disable()
		r.effectiveDelayNS.Store(0)
		r.realtimeResync.Store(false)
	}
	dropped := r.drainPublishQueue()
	bufferCleared := false
	if shouldClearBuffer {
		r.buffer.Clear()
		bufferCleared = true
	}
	r.logger.Info("input cleanup completed after stream ended", "previous_delay", delayValue, "previous_effective_delay", effectiveDelay, "delay_reset", shouldResetDelay, "dropped_packets", dropped, "buffer_cleared", bufferCleared, "status", "ok")
}

func (r *Reader) runOnce(ctx context.Context) error {
	parsedURL, err := url.Parse(r.url)
	if err != nil {
		return fmt.Errorf("parse RTMP input URL: %w", err)
	}

	client := &gortmplib.Client{
		URL:     parsedURL,
		Publish: false,
	}

	r.logger.Info("connecting to RTMP input", "url", r.url)
	if err := client.Initialize(ctx); err != nil {
		return fmt.Errorf("connect to RTMP input: %w", err)
	}
	netutil.EnableTCPNoDelay(client.NetConn())
	defer client.Close()

	r.connected.Store(true)
	r.connectedAt.Store(time.Now().UnixNano())
	r.audioPackets.Store(0)
	r.videoPackets.Store(0)
	r.bytesRead.Store(0)
	r.keyframeInterval.Store(0)
	r.lastKeyframeDTS.Store(0)
	r.lastKeyframeAt.Store(0)
	defer r.connected.Store(false)

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			client.Close()
		case <-done:
		}
	}()

	reader := &gortmplib.Reader{Conn: client}
	if err := reader.Initialize(); err != nil {
		return fmt.Errorf("initialize RTMP reader: %w", err)
	}

	r.logger.Info("connected to RTMP input", "tracks", len(reader.Tracks()), "delay", r.currentDelay(), "delay_enabled", r.delayIsEnabled(), "status", "ok")
	r.logTracks(reader)

	for {
		if r.readTimeout > 0 {
			if err := client.NetConn().SetReadDeadline(time.Now().Add(r.readTimeout)); err != nil {
				return fmt.Errorf("set RTMP read deadline: %w", err)
			}
		}

		msg, err := reader.Conn.Read()
		if err != nil {
			if ctx.Err() != nil || isClosedConnection(err) {
				return nil
			}
			return fmt.Errorf("read RTMP packet: %w", err)
		}

		r.handleMessage(msg)
		r.bytesRead.Store(client.BytesReceived())
	}
}

func (r *Reader) logTracks(reader *gortmplib.Reader) {
	for _, track := range reader.Tracks() {
		switch codec := track.Codec.(type) {
		case *codecs.H264:
			r.videoCodec.Store("H264")
			width, height := h264Dimensions(codec.SPS)
			r.storeVideoDimensions(width, height)
			r.logger.Info("registered video track", "codec", "H264", "width", width, "height", height, "status", "ok")
		case *codecs.H265:
			r.videoCodec.Store("H265")
			width, height := h265Dimensions(codec.SPS)
			r.storeVideoDimensions(width, height)
			r.logger.Info("registered video track", "codec", "H265", "width", width, "height", height, "status", "ok")
		case *codecs.AV1:
			r.videoCodec.Store("AV1")
			r.logger.Info("registered video track", "codec", "AV1", "status", "ok")
		case *codecs.VP9:
			r.videoCodec.Store("VP9")
			r.logger.Info("registered video track", "codec", "VP9", "status", "ok")
		case *codecs.MPEG4Audio:
			r.audioCodec.Store("MPEG4Audio/AAC")
			r.logger.Info("registered audio track", "codec", "MPEG4Audio/AAC", "status", "ok")
		case *codecs.MPEG1Audio:
			r.audioCodec.Store("MPEG1Audio")
			r.logger.Info("registered audio track", "codec", "MPEG1Audio", "status", "ok")
		case *codecs.Opus:
			r.audioCodec.Store("Opus")
			r.logger.Info("registered audio track", "codec", "Opus", "status", "ok")
		case *codecs.FLAC:
			r.audioCodec.Store("FLAC")
			r.logger.Info("registered audio track", "codec", "FLAC", "status", "ok")
		case *codecs.AC3:
			r.audioCodec.Store("AC3")
			r.logger.Info("registered audio track", "codec", "AC3", "status", "ok")
		case *codecs.G711:
			r.audioCodec.Store("G711")
			r.logger.Info("registered audio track", "codec", "G711", "status", "ok")
		case *codecs.LPCM:
			r.audioCodec.Store("LPCM")
			r.logger.Info("registered audio track", "codec", "LPCM", "status", "ok")
		default:
			r.logger.Warn("unsupported RTMP track codec", "codec", fmt.Sprintf("%T", track.Codec), "status", "warn")
		}
	}
}

func (r *Reader) storeVideoDimensions(width, height int) {
	if width > 0 {
		r.videoWidth.Store(int64(width))
	}
	if height > 0 {
		r.videoHeight.Store(int64(height))
	}
}

func h264Dimensions(sps []byte) (int, int) {
	if len(sps) == 0 {
		return 0, 0
	}
	var parsed h264.SPS
	if err := parsed.Unmarshal(sps); err != nil {
		return 0, 0
	}
	return parsed.Width(), parsed.Height()
}

func h265Dimensions(sps []byte) (int, int) {
	if len(sps) == 0 {
		return 0, 0
	}
	var parsed h265.SPS
	if err := parsed.Unmarshal(sps); err != nil {
		return 0, 0
	}
	return parsed.Width(), parsed.Height()
}

func (r *Reader) handleMessage(msg message.Message) {
	if isInitializationMessage(msg) {
		r.storeInitializationMessage(msg)
	}

	r.enqueueForPublish(msg)

	switch msg := msg.(type) {
	case *message.Video:
		r.onVideoMessage(videoCodecName(msg.Codec), msg.DTS+msg.PTSDelta, msg.DTS, msg.IsKeyFrame, msg.AU, msg)
	case *message.Audio:
		r.onAudioMessage(audioCodecName(msg.Codec), msg.DTS, msg.AU, msg)
	}
}

func (r *Reader) enqueueForPublish(msg message.Message) {
	if r.publisher != nil && shouldPublish(msg) {
		// While the newest decodable GOP is being fed during a return to live,
		// discard concurrently arriving packets. Otherwise they accumulate
		// behind the seed and permanently turn its duration into viewer delay.
		if r.realtimeSeedActive.Load() && r.realtimePassthroughMode() {
			return
		}
		r.publishMu.Lock()
		defer r.publishMu.Unlock()
		if r.slateActive.Load() {
			return
		}
		// In buffer modes, a separate cursor supplies only media that is the
		// requested distance behind live. Never mix current packets into it.
		if r.bufferDelayActive.Load() {
			return
		}

		// A return-to-live transition begins with a complete, decodable GOP.
		// Do not discard that seed merely because it is larger than the normal
		// realtime guard; doing so forces a second keyframe wait and can look
		// like a player rebuffer.
		seedProtected := r.realtimeSeedActive.Load()
		if r.realtimePassthroughMode() && !seedProtected && len(r.publishCh) > realtimeQueueLimit {
			dropped := r.drainPublishQueue()
			if dropped > 0 {
				r.realtimeDrops.Add(uint64(dropped))
				r.realtimeResync.Store(true)
				r.logger.Warn("realtime output skipped old queued packets", "dropped_packets", dropped, "queue_limit", realtimeQueueLimit, "status", "ok")
			}
		}

		select {
		case r.publishCh <- msg:
		default:
			r.logger.Error("publish queue is full; input keeps running", "queued", len(r.publishCh), "status", "error")
		}
	}
}

func (r *Reader) onVideoMessage(codec string, pts time.Duration, dts time.Duration, keyFrame bool, data []byte, msg message.Message) {
	r.videoPackets.Add(1)
	if keyFrame {
		previous := time.Duration(r.lastKeyframeDTS.Swap(int64(dts)))
		r.lastKeyframeAt.Store(time.Now().UnixNano())
		if previous > 0 && dts > previous {
			r.keyframeInterval.Store(int64(dts - previous))
		}
	}
	r.addToBuffer(media.Packet{
		Type:        media.PacketTypeVideo,
		Codec:       codec,
		PTS:         pts,
		DTS:         dts,
		IsKeyFrame:  keyFrame,
		ReceivedAt:  time.Now(),
		Data:        data,
		RTMPMessage: msg,
	})

	if r.firstVideo.CompareAndSwap(false, true) {
		r.logger.Info("received first video packet", "codec", codec, "pts", pts, "dts", dts, "keyframe", keyFrame, "status", "ok")
	}
}

func (r *Reader) onAudioMessage(codec string, pts time.Duration, data []byte, msg message.Message) {
	r.audioPackets.Add(1)
	r.addToBuffer(media.Packet{
		Type:        media.PacketTypeAudio,
		Codec:       codec,
		PTS:         pts,
		DTS:         pts,
		ReceivedAt:  time.Now(),
		Data:        data,
		RTMPMessage: msg,
	})

	if r.firstAudio.CompareAndSwap(false, true) {
		r.logger.Info("received first audio packet", "codec", codec, "pts", pts, "status", "ok")
	}
}

func (r *Reader) addToBuffer(packet media.Packet) {
	if r.buffer == nil {
		return
	}
	if packet.RTMPMessage != nil && isInitializationMessage(packet.RTMPMessage) {
		return
	}
	r.applyBufferRetention()
	if err := r.buffer.Add(packet); err != nil {
		r.logger.Error("failed to add packet to buffer", "error", err, "status", "error")
	}
}

func (r *Reader) applyBufferRetention() {
	retentionStore, ok := r.buffer.(buffer.RetentionStore)
	if !ok {
		return
	}

	window := activeBufferWindow
	if target := r.targetDelay() + 30*time.Second; target > window {
		window = target
	}
	if effective := r.effectiveDelay() + 30*time.Second; effective > window {
		window = effective
	}

	if r.bufferWindowNS.Swap(int64(window)) == int64(window) {
		return
	}
	retentionStore.SetMaxDuration(window)
	r.logger.Info("buffer retention adjusted", "max_duration", window, "status", "ok")
}

func (r *Reader) publishLoop(ctx context.Context) {
	var firstMediaDTS time.Duration
	var firstMediaWall time.Time
	var lastMediaDTS time.Duration
	var effectiveDelay time.Duration
	var lastDelayVersion uint64
	outputBaseInputDTS := time.Duration(0)
	outputBaseDTS := time.Duration(0)
	nextOutputBaseDTS := time.Duration(0)
	haveOutputBase := false
	lastPublishedDTS := time.Duration(0)
	haveLastPublishedDTS := false
	lastPublishedAudioDTS := time.Duration(0)
	lastPublishedVideoDTS := time.Duration(0)
	haveLastPublishedAudioDTS := false
	haveLastPublishedVideoDTS := false
	resumeAfterSlate := false
	bufferPump := time.NewTicker(25 * time.Millisecond)
	defer bufferPump.Stop()

	syncToRealtime := func(cmd syncLiveCommand) {
		r.publishMu.Lock()
		defer r.publishMu.Unlock()

		var realtimeMessages []message.Message
		if !cmd.resetOutput {
			realtimeMessages = r.realtimeSnapshotFromLatestKeyframe()
			if len(realtimeMessages) == 0 {
				cmd.done <- errors.New("no recent live keyframe is ready for seamless realtime switch")
				return
			}
		}

		dropped := r.drainPublishQueue()
		r.realtimeSeedLeft.Store(int64(len(realtimeMessages)))
		r.realtimeSeedActive.Store(len(realtimeMessages) > 0)
		for _, msg := range realtimeMessages {
			r.publishCh <- msg
		}
		resumeDTS := time.Duration(0)
		if haveLastPublishedDTS {
			resumeDTS = lastPublishedDTS + time.Millisecond
		}
		if r.delayState != nil {
			r.delayState.SetDelay(0)
			r.delayState.Disable()
		}
		effectiveDelay = 0
		r.effectiveDelayNS.Store(0)
		lastDelayVersion = r.delayVersion()
		firstMediaWall = time.Time{}
		firstMediaDTS = 0
		lastMediaDTS = 0
		haveOutputBase = false
		nextOutputBaseDTS = 0

		if cmd.resetOutput {
			if err := r.resetPublisherForRealtimeResume(ctx, cmd.resetPause); err != nil {
				r.logger.Error("failed to reset RTMP output for realtime resume", "error", err, "status", "error")
				cmd.done <- err
				return
			}
			lastPublishedDTS = 0
			haveLastPublishedDTS = false
			lastPublishedAudioDTS = 0
			lastPublishedVideoDTS = 0
			haveLastPublishedAudioDTS = false
			haveLastPublishedVideoDTS = false
			resumeDTS = 0
		} else if haveLastPublishedDTS {
			nextOutputBaseDTS = resumeDTS
			r.republishInitializationAt(ctx, nextOutputBaseDTS)
			lastPublishedAudioDTS = nextOutputBaseDTS
			lastPublishedVideoDTS = nextOutputBaseDTS
			haveLastPublishedAudioDTS = true
			haveLastPublishedVideoDTS = true
		}
		waitForKeyframe := cmd.resetOutput
		r.realtimeResync.Store(waitForKeyframe)
		r.logger.Info("live force-synchronized to realtime", "dropped_packets", dropped, "prepared_live_packets", len(realtimeMessages), "output_reset", cmd.resetOutput, "reset_pause", cmd.resetPause, "status", "ok")
		cmd.done <- nil
	}

	for {
		select {
		case cmd := <-r.syncLiveCh:
			syncToRealtime(cmd)
			continue
		default:
		}

		select {
		case <-ctx.Done():
			return
		case <-bufferPump.C:
			r.enqueueBufferedDelayRange(ctx)
		case cmd := <-r.armDelayCh:
			err := r.playSlateForDelayArm(ctx, cmd.request, &firstMediaWall, &firstMediaDTS, &lastMediaDTS, &effectiveDelay, &lastDelayVersion, &outputBaseInputDTS, &outputBaseDTS, &nextOutputBaseDTS, &haveOutputBase, &lastPublishedDTS, &haveLastPublishedDTS, &resumeAfterSlate)
			cmd.done <- err
		case cmd := <-r.bufferDelayCh:
			err := r.armDelayFromBuffer(ctx, cmd.delay, &firstMediaWall, &firstMediaDTS, &lastMediaDTS, &effectiveDelay, &lastDelayVersion, &outputBaseInputDTS, &outputBaseDTS, &nextOutputBaseDTS, &haveOutputBase, &lastPublishedDTS, &haveLastPublishedDTS, &resumeAfterSlate)
			cmd.done <- err
		case cmd := <-r.syncLiveCh:
			syncToRealtime(cmd)
		case msg := <-r.publishCh:
			realtimeSeedPacket := r.realtimeSeedLeft.Load() > 0
			lastRealtimeSeedPacket := false
			if realtimeSeedPacket {
				lastRealtimeSeedPacket = r.realtimeSeedLeft.Add(-1) == 0
			}
			if r.realtimePassthroughMode() && r.realtimeResync.Load() {
				if !isVideoKeyframeMessage(msg) {
					continue
				}
				r.realtimeResync.Store(false)
				firstMediaWall = time.Time{}
				r.logger.Info("realtime output resumed on keyframe", "status", "ok")
			}

			if mediaDTS, ok := mediaPayloadDTS(msg); ok {
				if firstMediaWall.IsZero() {
					firstMediaDTS = mediaDTS
					lastMediaDTS = mediaDTS
					effectiveDelay = r.targetDelay()
					if resumeAfterSlate {
						firstMediaWall = time.Now().Add(-effectiveDelay)
						resumeAfterSlate = false
					} else {
						firstMediaWall = time.Now()
					}
					r.effectiveDelayNS.Store(int64(effectiveDelay))
					lastDelayVersion = r.delayVersion()
					r.logger.Info("publish clock armed", "target_delay", r.targetDelay(), "effective_delay", effectiveDelay, "delay_enabled", r.delayIsEnabled(), "status", "ok")
				}

				mediaStep := mediaDTS - lastMediaDTS
				if mediaStep < 0 {
					mediaStep = 0
				}
				lastMediaDTS = mediaDTS

				version := r.delayVersion()
				if version != lastDelayVersion {
					lastDelayVersion = version
					r.logger.Info("dynamic delay target changed", "target_delay", r.targetDelay(), "effective_delay", effectiveDelay, "delay_enabled", r.delayIsEnabled(), "sync", delaySyncMode(effectiveDelay, r.targetDelay()), "status", "ok")
				}

				target := r.targetDelay()
				switch {
				case target > effectiveDelay:
					effectiveDelay = increaseEffectiveDelay(effectiveDelay, target, mediaStep)
					r.effectiveDelayNS.Store(int64(effectiveDelay))
				case target < effectiveDelay:
					effectiveDelay = decreaseEffectiveDelay(effectiveDelay, target, mediaStep)
					r.effectiveDelayNS.Store(int64(effectiveDelay))
				}
				if target == 0 && effectiveDelay == 0 && r.delayIsEnabled() {
					r.delayState.Disable()
					lastDelayVersion = r.delayVersion()
					r.logger.Info("smooth realtime catch-up finished", "status", "ok")
				}

				// Even at zero delay, keep the source media clock. Publishing each
				// packet immediately makes a 59.94fps source burst above real time,
				// which can make the ingest discard frames to catch up.
				waitUntil := firstMediaWall.Add(mediaDTS - firstMediaDTS).Add(effectiveDelay)
				// A realtime seed is a decodable GOP used only to bring the encoder
				// to the newest live frame. Pacing it at 1x would permanently add
				// that GOP duration to the viewer latency. Feed it immediately to
				// the downstream encoder, which keeps the Twitch output at 60 fps.
				if !realtimeSeedPacket {
					changed, err := r.sleepUntilOrDelayChange(ctx, waitUntil, lastDelayVersion)
					if err != nil {
						return
					}
					if changed {
						lastDelayVersion = r.delayVersion()
						r.logger.Info("dynamic delay target changed", "target_delay", r.targetDelay(), "effective_delay", effectiveDelay, "delay_enabled", r.delayIsEnabled(), "sync", delaySyncMode(effectiveDelay, r.targetDelay()), "status", "ok")
						continue
					}
				}
			}

			if mediaDTS, ok := mediaPayloadDTS(msg); ok && !haveOutputBase {
				outputBaseInputDTS = mediaDTS
				outputBaseDTS = nextOutputBaseDTS
				haveOutputBase = true
			}

			publishMsg, publishedDTS, hasPublishedDTS := prepareMessageForPublish(msg, outputBaseInputDTS, outputBaseDTS, haveOutputBase)
			publishMsg, publishedDTS, hasPublishedDTS = enforceMonotonicPublishedDTS(publishMsg, publishedDTS, hasPublishedDTS, &lastPublishedAudioDTS, &haveLastPublishedAudioDTS, &lastPublishedVideoDTS, &haveLastPublishedVideoDTS)
			if err := r.publisher.Publish(ctx, publishMsg); err != nil {
				r.realtimeSeedActive.Store(false)
				r.realtimeSeedLeft.Store(0)
				r.logger.Error("RTMP output publish failed; input keeps running", "error", err, "status", "error")
				r.reconnectPublisher(ctx)
				haveOutputBase = false
				if r.realtimePassthroughMode() {
					dropped := r.drainPublishQueue()
					if dropped > 0 {
						r.realtimeDrops.Add(uint64(dropped))
						r.logger.Warn("realtime output discarded stale packets after reconnect", "dropped_packets", dropped, "status", "ok")
					}
					r.realtimeResync.Store(true)
					firstMediaWall = time.Time{}
				}
				nextOutputBaseDTS = 0
				haveLastPublishedDTS = false
				haveLastPublishedAudioDTS = false
				haveLastPublishedVideoDTS = false
			} else if hasPublishedDTS {
				lastPublishedDTS = publishedDTS
				haveLastPublishedDTS = true
			}
			// Keep dropping concurrently arriving live packets until the final
			// prepared packet has actually reached the RTMP publisher. The queue
			// counter reaches zero before that write completes; opening the guard
			// there can create a second keyframe wait during the return to live.
			if lastRealtimeSeedPacket {
				// The prepared GOP is intentionally published without 1x pacing so
				// the decoder reaches the newest live frame while the viewer keeps
				// seeing the last decoded frame. Re-anchor the wall clock on the next
				// packet, otherwise the GOP duration becomes an artificial pause that
				// fills the realtime queue and forces another keyframe resync.
				resetPacingAfterRealtimeSeed(&firstMediaWall, &firstMediaDTS, &lastMediaDTS)
				r.realtimeSeedActive.Store(false)
				r.logger.Info("realtime pacing re-anchored after prepared GOP", "status", "ok")
			}
		}
	}
}

func resetPacingAfterRealtimeSeed(firstMediaWall *time.Time, firstMediaDTS, lastMediaDTS *time.Duration) {
	*firstMediaWall = time.Time{}
	*firstMediaDTS = 0
	*lastMediaDTS = 0
}

func (r *Reader) ForceRealtime(ctx context.Context) error {
	return r.forceRealtime(ctx, false, 0)
}

func (r *Reader) ForceRealtimeWithReset(ctx context.Context) error {
	return r.ForceRealtimeWithResetPause(ctx, 0)
}

func (r *Reader) ForceRealtimeWithResetPause(ctx context.Context, pause time.Duration) error {
	if pause < 0 {
		pause = 0
	}
	if pause > 20*time.Second {
		pause = 20 * time.Second
	}
	return r.forceRealtime(ctx, true, pause)
}

func (r *Reader) forceRealtime(ctx context.Context, resetOutput bool, resetPause time.Duration) error {
	r.bufferDelayActive.Store(false)
	if r.slateActive.Load() {
		return fmt.Errorf("delay is being armed with slate; wait for the loading video to finish before forcing realtime")
	}

	done := make(chan error, 1)
	cmd := syncLiveCommand{resetOutput: resetOutput, resetPause: resetPause, done: done}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case r.syncLiveCh <- cmd:
	case <-time.After(commandTimeout):
		return fmt.Errorf("RTMP publish loop is not ready")
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		return err
	case <-time.After(commandTimeout):
		return fmt.Errorf("RTMP publish loop did not confirm realtime sync")
	}
}

func (r *Reader) ArmDelay(ctx context.Context, request ArmDelayRequest) error {
	if request.Delay < 0 {
		request.Delay = 0
	}
	if request.Delay > maxDelayDuration {
		return fmt.Errorf("delay %s exceeds maximum supported delay %s", request.Delay, maxDelayDuration)
	}
	if request.SlatePath == "" {
		return fmt.Errorf("slate path is required")
	}
	done := make(chan error, 1)
	cmd := armDelayCommand{request: request, done: done}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case r.armDelayCh <- cmd:
	case <-time.After(commandTimeout):
		return fmt.Errorf("RTMP publish loop is not ready")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		return err
	}
}

func (r *Reader) ArmDelayFromBuffer(ctx context.Context, delayValue time.Duration) error {
	if delayValue < 0 {
		delayValue = 0
	}
	if delayValue > maxDelayDuration {
		return fmt.Errorf("delay %s exceeds maximum supported delay %s", delayValue, maxDelayDuration)
	}
	done := make(chan error, 1)
	cmd := bufferDelayCommand{delay: delayValue, done: done}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case r.bufferDelayCh <- cmd:
	case <-time.After(commandTimeout):
		return fmt.Errorf("RTMP publish loop is not ready")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		return err
	}
}

func (r *Reader) playSlateForDelayArm(
	ctx context.Context,
	request ArmDelayRequest,
	firstMediaWall *time.Time,
	firstMediaDTS *time.Duration,
	lastMediaDTS *time.Duration,
	effectiveDelay *time.Duration,
	lastDelayVersion *uint64,
	outputBaseInputDTS *time.Duration,
	outputBaseDTS *time.Duration,
	nextOutputBaseDTS *time.Duration,
	haveOutputBase *bool,
	lastPublishedDTS *time.Duration,
	haveLastPublishedDTS *bool,
	resumeAfterSlate *bool,
) error {
	r.bufferDelayActive.Store(false)
	cursorArmed := false
	defer func() {
		if !cursorArmed {
			r.bufferDelayActive.Store(false)
		}
	}()
	r.logger.Info("arming delay with slate", "delay", request.Delay, "slate", request.SlatePath, "play_full_slate", request.PlayFullSlate, "status", "starting")

	r.slateActive.Store(true)
	defer r.slateActive.Store(false)

	dropped := r.drainPublishQueue()
	if dropped > 0 {
		r.logger.Info("live packets held while slate starts", "dropped_packets", dropped, "status", "ok")
	}

	startDTS := time.Duration(0)
	if *haveLastPublishedDTS {
		startDTS = *lastPublishedDTS + time.Millisecond
	}

	player := &slate.Player{
		Path:       request.SlatePath,
		Logger:     r.logger.With("module", "slate"),
		Publisher:  r.publisher,
		ClockStart: time.Now(),
		ClockDTS:   startDTS,
	}
	slateBudget := request.Delay - slateVisualLead
	if !request.ShortSlate && slateBudget < time.Second {
		slateBudget = request.Delay
	}
	remainingSlate := slateBudget
	if request.ShortSlate || request.PlayFullSlate {
		// Short loading is a user-selected visual duration. Play the file once,
		// whatever its length, then enter the already prepared buffer.
		remainingSlate = 0
	} else if remainingSlate <= 0 {
		remainingSlate = time.Second
	}
	result, err := player.Play(ctx, startDTS, remainingSlate)
	if err != nil {
		return err
	}
	slateDuration := result.Duration
	for !request.ShortSlate && r.shouldContinueSlateForDelayArm(request, slateBudget, slateDuration) {
		remainingSlate = slateBudget - slateDuration
		if request.PlayFullSlate {
			remainingSlate = 0
		} else if remainingSlate <= 250*time.Millisecond {
			break
		}
		r.logger.Info("slate replaying while delay gets ready", "buffer_duration", r.bufferDuration(), "target_delay", request.Delay, "slate_duration", slateDuration, "minimum_visual", slateBudget, "status", "waiting")
		result, err = player.Play(ctx, result.LastDTS+time.Millisecond, remainingSlate)
		if err != nil {
			return err
		}
		slateDuration += result.Duration
	}
	*lastPublishedDTS = result.LastDTS
	*haveLastPublishedDTS = true
	liveResumeDTS := result.LastDTS + time.Millisecond
	r.republishInitializationAt(ctx, liveResumeDTS)

	if err := r.waitForBufferDuration(ctx, request.Delay); err != nil {
		return err
	}

	// The slate kept live packets out of the publisher while it played. Resume
	// through the same bounded, continuous cursor used by buffer entry instead
	// of inserting a full delayed snapshot into the queue.
	queued := 0
	snapshotDelay := time.Duration(0)
	if request.Delay > 0 {
		r.bufferDelayActive.Store(true)
		r.bufferDelayCursor.Store(0)
		queued, snapshotDelay = r.enqueueInitialBufferedDelayRange(ctx, request.Delay)
		if queued == 0 {
			return fmt.Errorf("no buffered packets available for delayed resume")
		}
		r.logger.Info("buffered cursor queued for delayed resume", "packets", queued, "target_delay", request.Delay, "snapshot_delay", snapshotDelay, "slate_duration", slateDuration, "status", "ok")
	}

	if r.delayState != nil {
		r.delayState.SetDelay(request.Delay)
		r.delayState.Enable()
	}
	*effectiveDelay = request.Delay
	r.effectiveDelayNS.Store(int64(*effectiveDelay))
	*lastDelayVersion = r.delayVersion()
	*firstMediaWall = time.Time{}
	*firstMediaDTS = 0
	*lastMediaDTS = 0
	*outputBaseInputDTS = 0
	*outputBaseDTS = 0
	*nextOutputBaseDTS = liveResumeDTS + time.Millisecond
	*haveOutputBase = false
	*resumeAfterSlate = true
	cursorArmed = request.Delay > 0

	r.logger.Info("delay armed with slate", "delay", request.Delay, "status", "ok")
	return nil
}

func (r *Reader) armDelayFromBuffer(
	ctx context.Context,
	delayValue time.Duration,
	firstMediaWall *time.Time,
	firstMediaDTS *time.Duration,
	lastMediaDTS *time.Duration,
	effectiveDelay *time.Duration,
	lastDelayVersion *uint64,
	outputBaseInputDTS *time.Duration,
	outputBaseDTS *time.Duration,
	nextOutputBaseDTS *time.Duration,
	haveOutputBase *bool,
	lastPublishedDTS *time.Duration,
	haveLastPublishedDTS *bool,
	resumeAfterSlate *bool,
) error {
	r.logger.Info("arming delay from buffer", "delay", delayValue, "status", "starting")
	if delayValue <= 0 {
		return fmt.Errorf("buffer delay mode requires a delay greater than zero")
	}
	if r.buffer == nil {
		return fmt.Errorf("buffer is not available")
	}
	// Stop accepting current live packets before taking the first delayed
	// range. From this point the cursor below is the only source for output.
	r.bufferDelayActive.Store(true)
	r.bufferDelayCursor.Store(0)
	armed := false
	defer func() {
		if !armed {
			r.bufferDelayActive.Store(false)
		}
	}()

	dropped := r.drainPublishQueue()
	if dropped > 0 {
		r.logger.Info("live packets held while buffer delay starts", "dropped_packets", dropped, "status", "ok")
	}

	liveResumeDTS := time.Duration(0)
	if *haveLastPublishedDTS {
		liveResumeDTS = *lastPublishedDTS + time.Millisecond
	}
	// Direct buffer entry jumps to an older keyframe from the same OBS bitstream.
	// Re-sending AVC/AAC initialization here makes RTMP/HLS relays interpret the
	// jump as a codec boundary and can force a viewer rebuffer. Keep the existing
	// decoder configuration and only remap the delayed GOP onto the continuous
	// output clock below. Slate transitions still republish initialization because
	// they really switch from the media file back to the OBS bitstream.

	if err := r.waitForBufferDuration(ctx, delayValue); err != nil {
		return err
	}

	queued, snapshotDelay := r.enqueueInitialBufferedDelayRange(ctx, delayValue)
	if queued == 0 {
		return fmt.Errorf("no buffered packets available for delay %s", delayValue)
	}
	r.logger.Info("buffered packets queued for direct delayed resume", "packets", queued, "target_delay", delayValue, "snapshot_delay", snapshotDelay, "status", "ok")

	if r.delayState != nil {
		r.delayState.SetDelay(delayValue)
		r.delayState.Enable()
	}
	*effectiveDelay = delayValue
	r.effectiveDelayNS.Store(int64(*effectiveDelay))
	*lastDelayVersion = r.delayVersion()
	*firstMediaWall = time.Time{}
	*firstMediaDTS = 0
	*lastMediaDTS = 0
	*outputBaseInputDTS = 0
	*outputBaseDTS = 0
	*nextOutputBaseDTS = liveResumeDTS + time.Millisecond
	*haveOutputBase = false
	*resumeAfterSlate = true
	armed = true

	r.logger.Info("delay armed from buffer", "delay", delayValue, "status", "ok")
	return nil
}

func (r *Reader) enqueueInitialBufferedDelayRange(ctx context.Context, targetDelay time.Duration) (int, time.Duration) {
	delayedStore, ok := r.buffer.(buffer.DelayedStore)
	if !ok {
		return r.enqueueDelayedSnapshot(ctx, targetDelay)
	}
	var packets []media.Packet
	var snapshotDelay time.Duration
	var err error
	if prefetchStore, ok := r.buffer.(buffer.PrefetchDelayedStore); ok {
		packets, snapshotDelay, err = prefetchStore.DelayedInitialRange(targetDelay, delayedCursorPrefetch)
	} else {
		packets, snapshotDelay, err = delayedStore.DelayedRange(targetDelay, 0, true)
	}
	if err != nil {
		r.logger.Error("failed to read initial delayed buffer range", "error", err, "status", "error")
		return 0, 0
	}
	if len(packets) > 0 {
		r.logger.Info("initial delayed cursor range prepared", "range_start", packets[0].Timestamp(), "range_end", packets[len(packets)-1].Timestamp(), "target_delay", targetDelay, "prefetch", delayedCursorPrefetch, "status", "ok")
	}
	queued := r.enqueueBufferedPackets(ctx, packets, true)

	// The first keyframe range can be shorter than the desired prefetch when a
	// GOP ends near the delayed point. Fetch the following cursor range now,
	// before returning from the slate, instead of waiting for the next pump
	// tick. This gives the Twitch player several seconds of continuous media at
	// the exact moment the short loading video ends.
	if queued > 0 && targetDelay > delayedCursorPrefetch {
		after := time.Duration(r.bufferDelayCursor.Load())
		following, _, followErr := delayedStore.DelayedRange(targetDelay-delayedCursorPrefetch, after, false)
		if followErr != nil {
			r.logger.Error("failed to prebuffer delayed cursor after slate", "error", followErr, "status", "error")
		} else if len(following) > 0 {
			added := r.enqueueBufferedPackets(ctx, following, false)
			queued += added
			if added > 0 {
				r.logger.Info("delayed cursor transition prebuffer prepared", "packets", added, "range_end", following[len(following)-1].Timestamp(), "prefetch", delayedCursorPrefetch, "status", "ok")
			}
		}
	}

	return queued, snapshotDelay
}

func (r *Reader) enqueueBufferedDelayRange(ctx context.Context) {
	if !r.bufferDelayActive.Load() || r.buffer == nil {
		return
	}
	if len(r.publishCh) >= directDelayQueueLimit {
		return
	}
	delayedStore, ok := r.buffer.(buffer.DelayedStore)
	if !ok {
		return
	}
	after := time.Duration(r.bufferDelayCursor.Load())
	// The initial transition deliberately queues delayedCursorPrefetch ahead of
	// the playback point. Keep using that same availability horizon while the
	// cursor runs. Fetching again at the exact target delay leaves the cursor
	// ahead of the store boundary for roughly three seconds, so the prepared
	// queue drains and the downstream encoder temporarily receives no frames.
	// Packets fetched ahead are still paced at 1x by publishLoop; this is media
	// headroom only and does not reduce the configured viewer delay.
	fetchDelay := delayedCursorFetchDelay(r.targetDelay())
	packets, _, err := delayedStore.DelayedRange(fetchDelay, after, false)
	if err != nil {
		r.logger.Error("failed to extend delayed buffer cursor", "error", err, "status", "error")
		return
	}
	if len(packets) == 0 {
		return
	}
	queued := r.enqueueBufferedPackets(ctx, packets, false)
	if queued > 0 {
		r.logger.Debug("delayed buffer cursor advanced", "packets", queued, "status", "ok")
	}
}

func delayedCursorFetchDelay(targetDelay time.Duration) time.Duration {
	if targetDelay > delayedCursorPrefetch {
		return targetDelay - delayedCursorPrefetch
	}
	return targetDelay
}

func (r *Reader) enqueueBufferedPackets(ctx context.Context, packets []media.Packet, initial bool) int {
	queued := 0
	available := directDelayQueueLimit - len(r.publishCh)
	if available <= 0 {
		return 0
	}
	var last time.Duration
	for _, packet := range packets {
		if queued >= available {
			break
		}
		if packet.RTMPMessage == nil || !shouldPublish(packet.RTMPMessage) || isInitializationMessage(packet.RTMPMessage) {
			continue
		}
		select {
		case <-ctx.Done():
			return queued
		case r.publishCh <- packet.RTMPMessage:
			queued++
			if packet.Timestamp() > last {
				last = packet.Timestamp()
			}
		}
	}
	if last > 0 {
		r.bufferDelayCursor.Store(int64(last))
	}
	return queued
}

func (r *Reader) shouldContinueSlateForDelayArm(request ArmDelayRequest, minimumVisual time.Duration, slateDuration time.Duration) bool {
	bufferReady := request.Delay <= 0 || r.buffer == nil || r.buffer.Duration() >= request.Delay
	visualReady := minimumVisual <= 0 || slateDuration >= minimumVisual
	return !bufferReady || !visualReady
}

func (r *Reader) bufferDuration() time.Duration {
	if r.buffer == nil {
		return 0
	}
	return r.buffer.Duration()
}

func (r *Reader) enqueueDelayedSnapshot(ctx context.Context, targetDelay time.Duration) (int, time.Duration) {
	if targetDelay <= 0 || r.buffer == nil {
		return 0, 0
	}

	var snapshot []media.Packet
	var snapshotDelay time.Duration
	var err error
	if delayedStore, ok := r.buffer.(buffer.DelayedStore); ok {
		snapshot, snapshotDelay, err = delayedStore.DelayedSnapshot(targetDelay)
		if err != nil {
			r.logger.Error("failed to read delayed disk buffer snapshot", "error", err, "status", "error")
			return 0, 0
		}
	} else {
		snapshot = r.buffer.Snapshot()
	}
	if len(snapshot) == 0 {
		return 0, 0
	}

	start := 0
	if snapshotDelay == 0 {
		latest := snapshot[len(snapshot)-1].Timestamp()
		target := latest - targetDelay
		if target < 0 {
			target = 0
		}

		for i, packet := range snapshot {
			if packet.Timestamp() >= target {
				start = i
				break
			}
		}

		for i := start; i >= 0; i-- {
			if snapshot[i].Type == media.PacketTypeVideo && snapshot[i].IsKeyFrame && snapshot[i].RTMPMessage != nil {
				start = i
				break
			}
		}
		snapshotDelay = latest - snapshot[start].Timestamp()
	}

	queued := 0
	for _, packet := range snapshot[start:] {
		if packet.RTMPMessage == nil || !shouldPublish(packet.RTMPMessage) || isInitializationMessage(packet.RTMPMessage) {
			continue
		}
		select {
		case <-ctx.Done():
			return queued, snapshotDelay
		case r.publishCh <- packet.RTMPMessage:
			queued++
		}
	}

	return queued, snapshotDelay
}

func (r *Reader) waitForBufferDuration(ctx context.Context, target time.Duration) error {
	if target <= 0 || r.buffer == nil {
		return nil
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if r.buffer.Duration() >= target {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func increaseEffectiveDelay(current time.Duration, target time.Duration, mediaStep time.Duration) time.Duration {
	if current >= target {
		return target
	}

	step := syncUpStep(mediaStep)
	next := current + step
	if next > target {
		return target
	}
	return next
}

func decreaseEffectiveDelay(current time.Duration, target time.Duration, mediaStep time.Duration) time.Duration {
	if current <= target {
		return target
	}

	step := syncDownStep(mediaStep)
	next := current - step
	if next < target {
		return target
	}
	return next
}

func syncUpStep(mediaStep time.Duration) time.Duration {
	step := mediaStep / 4
	if step < 5*time.Millisecond {
		step = 5 * time.Millisecond
	}
	if step > 40*time.Millisecond {
		step = 40 * time.Millisecond
	}
	return step
}

func syncDownStep(mediaStep time.Duration) time.Duration {
	step := mediaStep / 6
	if step < 2*time.Millisecond {
		step = 2 * time.Millisecond
	}
	if step > 12*time.Millisecond {
		step = 12 * time.Millisecond
	}
	return step
}

func readyToResumeAfterDelayDecrease(target time.Duration, queuedPackets int) bool {
	if target <= 0 {
		return queuedPackets <= 32
	}
	return queuedPackets <= 256
}

func (r *Reader) resetPublisherForRealtimeResume(ctx context.Context, pause time.Duration) error {
	if r.publisher == nil {
		return nil
	}

	_ = r.publisher.Close()
	if pause > 0 {
		r.logger.Info("RTMP output paused before realtime reconnect", "pause", pause, "status", "waiting")
		timer := time.NewTimer(pause)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	if err := r.publisher.Connect(ctx); err != nil {
		return err
	}
	r.republishInitialization(ctx)
	r.logger.Info("RTMP output reset for realtime resume", "status", "ok")
	return nil
}

func (r *Reader) reconnectPublisherForSlate(ctx context.Context) error {
	if r.publisher == nil {
		return nil
	}
	_ = r.publisher.Close()
	if err := r.publisher.Connect(ctx); err != nil {
		return err
	}
	r.logger.Info("RTMP output reconnected for slate", "status", "ok")
	return nil
}

func (r *Reader) reconnectPublisher(ctx context.Context) {
	if r.publisher == nil {
		return
	}

	_ = r.publisher.Close()
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.publisher.Connect(ctx); err != nil {
				r.logger.Error("RTMP output reconnect failed; input still running", "error", err, "status", "error")
				continue
			}
			r.logger.Info("RTMP output reconnected", "status", "ok")
			r.republishInitialization(ctx)
			return
		}
	}
}

func (r *Reader) republishInitialization(ctx context.Context) {
	r.republishInitializationAt(ctx, 0)
}

func (r *Reader) republishInitializationAt(ctx context.Context, dts time.Duration) {
	if r.publisher == nil {
		return
	}

	snapshot := r.initializationSnapshot()
	if len(snapshot) == 0 && r.buffer != nil {
		for _, packet := range r.buffer.Snapshot() {
			if packet.RTMPMessage != nil && isInitializationMessage(packet.RTMPMessage) {
				snapshot = append(snapshot, packet.RTMPMessage)
			}
		}
	}

	for _, msg := range snapshot {
		if err := r.publisher.Publish(ctx, prepareInitializationMessageForPublishAtDTS(msg, dts)); err != nil {
			r.logger.Error("failed to republish RTMP initialization packet", "error", err, "status", "error")
			return
		}
	}
	if len(snapshot) > 0 {
		r.logger.Info("RTMP initialization packets republished", "packets", len(snapshot), "dts", dts, "status", "ok")
	} else {
		r.logger.Warn("no RTMP initialization packets available to republish", "status", "warn")
	}
}

func (r *Reader) sleepUntilOrDelayChange(ctx context.Context, target time.Time, version uint64) (bool, error) {
	for {
		if r.delayVersion() != version {
			return true, nil
		}

		wait := time.Until(target)
		if wait <= 0 {
			return false, nil
		}
		if wait > 25*time.Millisecond {
			wait = 25 * time.Millisecond
		}

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false, ctx.Err()
		case <-timer.C:
		}
	}
}

func prepareInitializationMessageForPublish(msg message.Message) message.Message {
	return prepareInitializationMessageForPublishAtDTS(msg, 0)
}

func prepareInitializationMessageForPublishAtDTS(msg message.Message, dts time.Duration) message.Message {
	switch msg := msg.(type) {
	case *message.Audio:
		clone := *msg
		clone.DTS = dts
		return &clone
	case *message.Video:
		clone := *msg
		clone.DTS = dts
		return &clone
	default:
		return msg
	}
}

func prepareMessageForPublish(msg message.Message, baseInputDTS time.Duration, baseOutputDTS time.Duration, haveBase bool) (message.Message, time.Duration, bool) {
	if !haveBase {
		if isInitializationMessage(msg) {
			return prepareInitializationMessageForPublish(msg), 0, false
		}
		return msg, 0, false
	}

	switch msg := msg.(type) {
	case *message.Audio:
		clone := *msg
		clone.DTS = normalizeOutputDTS(clone.DTS, baseInputDTS, baseOutputDTS)
		return &clone, clone.DTS, true
	case *message.Video:
		clone := *msg
		clone.DTS = normalizeOutputDTS(clone.DTS, baseInputDTS, baseOutputDTS)
		return &clone, clone.DTS, true
	default:
		return msg, 0, false
	}
}

func enforceMonotonicPublishedDTS(msg message.Message, dts time.Duration, hasDTS bool, lastAudio *time.Duration, haveAudio *bool, lastVideo *time.Duration, haveVideo *bool) (message.Message, time.Duration, bool) {
	if !hasDTS {
		return msg, dts, hasDTS
	}

	switch typed := msg.(type) {
	case *message.Audio:
		if *haveAudio && dts <= *lastAudio {
			dts = *lastAudio + time.Millisecond
			clone := *typed
			clone.DTS = dts
			msg = &clone
		}
		*lastAudio = dts
		*haveAudio = true
	case *message.Video:
		if *haveVideo && dts <= *lastVideo {
			dts = *lastVideo + time.Millisecond
			clone := *typed
			clone.DTS = dts
			msg = &clone
		}
		*lastVideo = dts
		*haveVideo = true
	}

	return msg, dts, true
}

func normalizeOutputDTS(inputDTS time.Duration, baseInputDTS time.Duration, baseOutputDTS time.Duration) time.Duration {
	if inputDTS < baseInputDTS {
		return baseOutputDTS
	}
	return baseOutputDTS + inputDTS - baseInputDTS
}

func mediaPayloadDTS(msg message.Message) (time.Duration, bool) {
	switch msg := msg.(type) {
	case *message.Audio:
		if msg.Codec == message.CodecMPEG4Audio && msg.AACType != message.AudioAACTypeAU {
			return 0, false
		}
		return msg.DTS, true
	case *message.Video:
		if msg.Type != message.VideoTypeAU {
			return 0, false
		}
		return msg.DTS, true
	default:
		return 0, false
	}
}

func (r *Reader) logStats(done <-chan struct{}) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			audio := r.audioPackets.Load()
			video := r.videoPackets.Load()
			bytesRead := r.bytesRead.Load()
			bufferStats := buffer.Stats{}
			if r.buffer != nil {
				bufferStats = r.buffer.Stats()
			}
			outputStats := output.Stats{}
			if r.publisher != nil {
				outputStats = r.publisher.Stats()
			}

			r.logger.Info(
				"RTMP pipeline status",
				"status", statusOK(audio > 0 && video > 0 && bufferStats.Packets > 0 && outputStats.Connected),
				"input", statusOK(audio > 0 && video > 0),
				"buffer", statusOK(bufferStats.Packets > 0),
				"output", statusOK(outputStats.Connected && outputStats.AudioPackets > 0 && outputStats.VideoPackets > 0),
				"delay_enabled", r.delayIsEnabled(),
				"target_delay", r.targetDelay(),
				"effective_delay", r.effectiveDelay(),
				"sync", delaySyncMode(r.effectiveDelay(), r.targetDelay()),
				"sync_note", delaySyncNote(r.effectiveDelay(), r.targetDelay()),
				"output_queue", len(r.publishCh),
				"audio_packets", audio,
				"video_packets", video,
				"bytes_read", bytesRead,
				"buffer_packets", bufferStats.Packets,
				"buffer_duration", bufferStats.Duration,
				"buffer_bytes", bufferStats.Bytes,
				"published_audio", outputStats.AudioPackets,
				"published_video", outputStats.VideoPackets,
				"published_bytes", outputStats.BytesPublished,
			)

		case <-done:
			return
		}
	}
}

func isResumePoint(msg message.Message, hasVideo bool) bool {
	switch msg := msg.(type) {
	case *message.Video:
		return msg.Type == message.VideoTypeAU && msg.IsKeyFrame
	case *message.Audio:
		return !hasVideo
	default:
		return false
	}
}

func (r *Reader) storeInitializationMessage(msg message.Message) {
	kind := initializationKind(msg)
	if kind == "" {
		return
	}

	r.initMu.Lock()
	defer r.initMu.Unlock()

	for i, existing := range r.initMessages {
		if initializationKind(existing) == kind {
			r.initMessages[i] = msg
			return
		}
	}
	r.initMessages = append(r.initMessages, msg)
}

func (r *Reader) initializationSnapshot() []message.Message {
	r.initMu.RLock()
	defer r.initMu.RUnlock()

	snapshot := make([]message.Message, len(r.initMessages))
	copy(snapshot, r.initMessages)
	return snapshot
}

func initializationKind(msg message.Message) string {
	switch msg := msg.(type) {
	case *message.DataAMF0:
		return "data"
	case *message.Video:
		if msg.Type == message.VideoTypeConfig {
			return fmt.Sprintf("video-config-%d", msg.Codec)
		}
	case *message.Audio:
		if msg.Codec == message.CodecMPEG4Audio && msg.AACType == message.AudioAACTypeConfig {
			return fmt.Sprintf("audio-config-%d", msg.Codec)
		}
	}
	return ""
}

func isInitializationMessage(msg message.Message) bool {
	switch msg := msg.(type) {
	case *message.DataAMF0:
		return true
	case *message.Video:
		return msg.Type == message.VideoTypeConfig
	case *message.Audio:
		return msg.Codec == message.CodecMPEG4Audio && msg.AACType == message.AudioAACTypeConfig
	default:
		return false
	}
}

func (r *Reader) realtimePassthroughMode() bool {
	return r.publisher != nil && !r.delayIsEnabled()
}

func (r *Reader) drainPublishQueue() int {
	dropped := 0
	for {
		select {
		case <-r.publishCh:
			dropped++
		default:
			return dropped
		}
	}
}

// realtimeSnapshotFromLatestKeyframe prepares a decodable live GOP before the
// delayed queue is replaced. This keeps the old picture flowing until the new
// live picture is already available and avoids an empty/undecodable interval.
func (r *Reader) realtimeSnapshotFromLatestKeyframe() []message.Message {
	if r.buffer == nil {
		return nil
	}

	var snapshot []media.Packet
	if latestStore, ok := r.buffer.(buffer.LatestKeyframeStore); ok {
		packets, err := latestStore.LatestKeyframeSnapshot()
		if err != nil {
			r.logger.Warn("failed to read latest keyframe snapshot", "error", err, "status", "warning")
			return nil
		}
		snapshot = packets
	} else {
		snapshot = r.buffer.Snapshot()
	}
	start := -1
	for i := len(snapshot) - 1; i >= 0; i-- {
		packet := snapshot[i]
		if packet.Type == media.PacketTypeVideo && packet.IsKeyFrame && packet.RTMPMessage != nil {
			start = i
			break
		}
	}
	if start < 0 {
		return nil
	}

	messages := make([]message.Message, 0, len(snapshot)-start)
	for _, packet := range snapshot[start:] {
		if packet.RTMPMessage != nil && isMediaPayloadMessage(packet.RTMPMessage) {
			messages = append(messages, packet.RTMPMessage)
		}
	}
	return messages
}

func isVideoKeyframeMessage(msg message.Message) bool {
	video, ok := msg.(*message.Video)
	return ok && video.Type == message.VideoTypeAU && video.IsKeyFrame
}

func isMediaPayloadMessage(msg message.Message) bool {
	_, ok := mediaPayloadDTS(msg)
	return ok
}

func shouldPublish(msg message.Message) bool {
	switch msg.(type) {
	case *message.Audio, *message.Video, *message.DataAMF0:
		return true
	default:
		return false
	}
}

func (r *Reader) delayIsEnabled() bool {
	if r.delayState == nil {
		return false
	}
	return r.delayState.Enabled()
}

func (r *Reader) currentDelay() time.Duration {
	if r.delayState == nil {
		return 0
	}
	return r.delayState.Delay()
}

func (r *Reader) targetDelay() time.Duration {
	if !r.delayIsEnabled() {
		return 0
	}
	return r.currentDelay()
}

func (r *Reader) effectiveDelay() time.Duration {
	return time.Duration(r.effectiveDelayNS.Load())
}

func (r *Reader) delayVersion() uint64 {
	if r.delayState == nil {
		return 0
	}
	return r.delayState.Version()
}

func delaySyncNote(effective time.Duration, target time.Duration) string {
	if effective > target {
		return "catch-up limited to avoid player overflow"
	}
	return ""
}

func delaySyncMode(effective time.Duration, target time.Duration) string {
	switch {
	case effective == target:
		return "stable"
	case effective < target:
		return "increasing"
	default:
		return "catching_up"
	}
}

func statusOK(ok bool) string {
	if ok {
		return "ok"
	}
	return "waiting"
}

func videoCodecName(codec uint8) string {
	switch codec {
	case message.CodecH264:
		return "H264"
	case message.CodecH265:
		return "H265"
	default:
		return fmt.Sprintf("video/%d", codec)
	}
}

func audioCodecName(codec uint8) string {
	switch codec {
	case message.CodecMPEG4Audio:
		return "MPEG4Audio/AAC"
	case message.CodecMPEG1Audio:
		return "MPEG1Audio"
	case message.CodecPCMA:
		return "PCMA"
	case message.CodecPCMU:
		return "PCMU"
	case message.CodecLPCM:
		return "LPCM"
	default:
		return fmt.Sprintf("audio/%d", codec)
	}
}

func (r *Reader) Stats() input.Stats {
	keyframeInterval := time.Duration(r.keyframeInterval.Load())
	keyframeAge := time.Duration(0)
	if lastKeyframeAt := r.lastKeyframeAt.Load(); lastKeyframeAt > 0 {
		keyframeAge = time.Since(time.Unix(0, lastKeyframeAt))
	}
	videoPackets := r.videoPackets.Load()
	bytesRead := r.bytesRead.Load()
	elapsed := time.Duration(0)
	if connectedAt := r.connectedAt.Load(); connectedAt > 0 {
		elapsed = time.Since(time.Unix(0, connectedAt))
	}
	fps := 0.0
	bitrateKbps := 0.0
	if elapsed > 0 {
		seconds := elapsed.Seconds()
		fps = float64(videoPackets) / seconds
		bitrateKbps = float64(bytesRead*8) / seconds / 1000
	}
	videoCodec, _ := r.videoCodec.Load().(string)
	audioCodec, _ := r.audioCodec.Load().(string)
	queue := len(r.publishCh)
	realtimeState := "aguardando"
	if r.connected.Load() {
		switch {
		case r.delayIsEnabled():
			realtimeState = "delay manual"
		case r.realtimeResync.Load():
			realtimeState = "ressincronizando"
		case queue > realtimeQueueHealthyLimit:
			realtimeState = "recuperando"
		default:
			realtimeState = "pareado"
		}
	}
	return input.Stats{
		Connected:           r.connected.Load(),
		AudioPackets:        r.audioPackets.Load(),
		VideoPackets:        videoPackets,
		BytesRead:           bytesRead,
		VideoCodec:          videoCodec,
		AudioCodec:          audioCodec,
		Width:               int(r.videoWidth.Load()),
		Height:              int(r.videoHeight.Load()),
		FPS:                 fps,
		BitrateKbps:         bitrateKbps,
		KeyframeInterval:    keyframeInterval.String(),
		KeyframeIntervalSec: keyframeInterval.Seconds(),
		KeyframeAge:         keyframeAge,
		OutputQueue:         queue,
		RealtimeState:       realtimeState,
		RealtimeDrops:       r.realtimeDrops.Load(),
	}
}

func isClosedConnection(err error) bool {
	if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return false
	}

	return false
}
