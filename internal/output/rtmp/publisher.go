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

	"delayengine/internal/config"
	"delayengine/internal/netutil"
	"delayengine/internal/output"

	"github.com/bluenviron/gortmplib"
	"github.com/bluenviron/gortmplib/pkg/message"
)

type PublisherConfig struct {
	URL          string
	WriteTimeout time.Duration
	Logger       *slog.Logger
}

type Publisher struct {
	url          string
	writeTimeout time.Duration
	logger       *slog.Logger
	mu           sync.RWMutex
	client       *gortmplib.Client

	connected      atomic.Bool
	audioPackets   atomic.Uint64
	videoPackets   atomic.Uint64
	bytesPublished atomic.Uint64
	firstAudio     atomic.Bool
	firstVideo     atomic.Bool
}

func NewPublisher(cfg PublisherConfig) *Publisher {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Publisher{
		url:          cfg.URL,
		writeTimeout: cfg.WriteTimeout,
		logger:       logger,
	}
}

func (p *Publisher) Connect(ctx context.Context) error {
	parsedURL, err := url.Parse(p.url)
	if err != nil {
		return fmt.Errorf("parse RTMP output URL: %w", err)
	}

	client := &gortmplib.Client{
		URL:     parsedURL,
		Publish: true,
	}

	p.logger.Info("connecting to RTMP output", "url", config.RedactURLForLog(p.url))
	if err := client.Initialize(ctx); err != nil {
		return fmt.Errorf("connect to RTMP output: %w", err)
	}

	netutil.EnableTCPNoDelay(client.NetConn())
	_ = client.NetConn().SetDeadline(time.Time{})
	p.mu.Lock()
	p.client = client
	p.mu.Unlock()
	p.connected.Store(true)
	go p.readControlMessages(client)
	p.logger.Info("connected to RTMP output", "url", config.RedactURLForLog(p.url), "status", "ok")
	return nil
}

func (p *Publisher) Publish(ctx context.Context, msg message.Message) error {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()

	if client == nil {
		return errors.New("RTMP output is not connected")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	if p.writeTimeout > 0 {
		if err := client.NetConn().SetWriteDeadline(time.Now().Add(p.writeTimeout)); err != nil {
			return fmt.Errorf("set RTMP write deadline: %w", err)
		}
	}

	if err := client.Write(msg); err != nil {
		p.connected.Store(false)
		if isClosedConnection(err) {
			return io.EOF
		}
		return fmt.Errorf("write RTMP output packet: %w", err)
	}

	p.updateStats(msg)
	p.bytesPublished.Store(client.BytesSent())
	return nil
}

func (p *Publisher) Close() error {
	p.mu.Lock()
	client := p.client
	p.client = nil
	p.mu.Unlock()

	if client != nil {
		client.Close()
	}
	p.connected.Store(false)
	return nil
}

func (p *Publisher) Stats() output.Stats {
	return output.Stats{
		Connected:      p.connected.Load(),
		AudioPackets:   p.audioPackets.Load(),
		VideoPackets:   p.videoPackets.Load(),
		BytesPublished: p.bytesPublished.Load(),
	}
}

func (p *Publisher) updateStats(msg message.Message) {
	switch msg.(type) {
	case *message.Audio:
		count := p.audioPackets.Add(1)
		if p.firstAudio.CompareAndSwap(false, true) {
			p.logger.Info("published first audio packet", "audio_packets", count, "status", "ok")
		}
	case *message.Video:
		count := p.videoPackets.Add(1)
		if p.firstVideo.CompareAndSwap(false, true) {
			p.logger.Info("published first video packet", "video_packets", count, "status", "ok")
		}
	}
}

func (p *Publisher) readControlMessages(client *gortmplib.Client) {
	for {
		if _, err := client.Read(); err != nil {
			p.mu.RLock()
			current := p.client == client
			p.mu.RUnlock()
			if current {
				p.connected.Store(false)
				p.logger.Error("RTMP output control reader stopped", "error", err, "status", "error")
			}
			return
		}
	}
}

func isClosedConnection(err error) bool {
	if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	return false
}
