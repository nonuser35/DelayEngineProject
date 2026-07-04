package slate

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"delayengine/internal/output"

	mp4 "github.com/abema/go-mp4"
	"github.com/bluenviron/gortmplib/pkg/message"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
)

type Player struct {
	Path      string
	Logger    *slog.Logger
	Publisher output.Publisher
}

type Result struct {
	Duration time.Duration
	LastDTS  time.Duration
	Messages uint64
}

func (p *Player) Play(ctx context.Context, startDTS time.Duration, maxDuration time.Duration) (Result, error) {
	if p.Publisher == nil {
		return Result{}, fmt.Errorf("slate publisher is nil")
	}

	file, err := os.Open(p.Path)
	if err != nil {
		return Result{}, fmt.Errorf("open slate FLV: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return Result{}, fmt.Errorf("stat slate FLV: %w", err)
	}

	reader := bufio.NewReader(file)
	if err := readHeader(reader); err != nil {
		return Result{}, err
	}

	var firstDTS time.Duration
	var lastDTS time.Duration
	var haveFirst bool
	var messages uint64
	startWall := time.Now()

	for {
		msg, dts, err := readTag(reader)
		if err != nil {
			if err == io.EOF {
				break
			}
			return Result{}, err
		}
		if msg == nil {
			continue
		}

		if !haveFirst {
			firstDTS = dts
			haveFirst = true
		}
		if maxDuration > 0 && dts-firstDTS > maxDuration {
			break
		}

		outDTS := startDTS + dts - firstDTS
		if outDTS < startDTS {
			outDTS = startDTS
		}

		waitUntil := startWall.Add(outDTS - startDTS)
		if err := sleepUntil(ctx, waitUntil); err != nil {
			return Result{}, err
		}

		msg = setMessageDTS(msg, outDTS)
		if err := p.Publisher.Publish(ctx, msg); err != nil {
			return Result{}, fmt.Errorf("publish slate packet: %w", err)
		}
		lastDTS = outDTS
		messages++
	}

	if p.Logger != nil {
		bitrateMbps := 0.0
		duration := lastDTS - startDTS
		if duration > 0 {
			bitrateMbps = float64(info.Size()*8) / duration.Seconds() / 1000000
		}
		p.Logger.Info("slate playback finished", "path", p.Path, "duration", duration, "messages", messages, "bitrate_mbps", bitrateMbps, "status", "ok")
	}

	return Result{Duration: lastDTS - startDTS, LastDTS: lastDTS, Messages: messages}, nil
}

func readHeader(r *bufio.Reader) error {
	header := make([]byte, 9)
	if _, err := io.ReadFull(r, header); err != nil {
		return fmt.Errorf("read FLV header: %w", err)
	}
	if string(header[:3]) != "FLV" {
		return fmt.Errorf("invalid FLV header")
	}
	dataOffset := binary.BigEndian.Uint32(header[5:9])
	if dataOffset > 9 {
		if _, err := io.CopyN(io.Discard, r, int64(dataOffset-9)); err != nil {
			return fmt.Errorf("skip FLV header extension: %w", err)
		}
	}
	if _, err := io.CopyN(io.Discard, r, 4); err != nil {
		return fmt.Errorf("read FLV previous tag size: %w", err)
	}
	return nil
}

func readTag(r *bufio.Reader) (message.Message, time.Duration, error) {
	header := make([]byte, 11)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, 0, err
	}

	tagType := header[0]
	size := int(uint32(header[1])<<16 | uint32(header[2])<<8 | uint32(header[3]))
	timestampMS := uint32(header[7])<<24 | uint32(header[4])<<16 | uint32(header[5])<<8 | uint32(header[6])
	dts := time.Duration(timestampMS) * time.Millisecond

	body := make([]byte, size)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, 0, fmt.Errorf("read FLV tag body: %w", err)
	}
	if _, err := io.CopyN(io.Discard, r, 4); err != nil {
		return nil, 0, fmt.Errorf("read FLV previous tag size: %w", err)
	}

	switch tagType {
	case 8:
		msg, err := audioMessage(body, dts)
		return msg, dts, err
	case 9:
		msg, err := videoMessage(body, dts)
		return msg, dts, err
	default:
		return nil, dts, nil
	}
}

func audioMessage(body []byte, dts time.Duration) (message.Message, error) {
	if len(body) < 2 {
		return nil, fmt.Errorf("invalid FLV audio tag")
	}
	codec := body[0] >> 4
	if codec != message.CodecMPEG4Audio {
		return nil, nil
	}

	msg := &message.Audio{
		ChunkStreamID:   message.AudioChunkStreamID,
		MessageStreamID: 0x1000000,
		DTS:             dts,
		Codec:           message.CodecMPEG4Audio,
		Rate:            message.AudioRate((body[0] >> 2) & 0x03),
		Depth:           message.AudioDepth((body[0] >> 1) & 0x01),
		IsStereo:        (body[0] & 0x01) != 0,
		AACType:         message.AudioAACType(body[1]),
	}

	if msg.AACType == message.AudioAACTypeConfig {
		cfg := &mpeg4audio.AudioSpecificConfig{}
		if len(body) > 2 {
			if err := cfg.Unmarshal(body[2:]); err != nil {
				return nil, fmt.Errorf("parse AAC config: %w", err)
			}
		}
		msg.AACConfig = cfg
	} else {
		msg.AU = body[2:]
	}
	return msg, nil
}

func videoMessage(body []byte, dts time.Duration) (message.Message, error) {
	if len(body) < 5 {
		return nil, fmt.Errorf("invalid FLV video tag")
	}
	codec := body[0] & 0x0F
	if codec != message.CodecH264 {
		return nil, fmt.Errorf("slate video codec must be H264, got %d", codec)
	}

	msg := &message.Video{
		ChunkStreamID:   message.VideoChunkStreamID,
		MessageStreamID: 0x1000000,
		DTS:             dts,
		Codec:           message.CodecH264,
		IsKeyFrame:      (body[0] >> 4) == 1,
		Type:            message.VideoType(body[1]),
		PTSDelta:        time.Duration(uint32(body[2])<<16|uint32(body[3])<<8|uint32(body[4])) * time.Millisecond,
	}

	if msg.Type == message.VideoTypeConfig {
		cfg := &mp4.AVCDecoderConfiguration{}
		cfg.SetType(mp4.BoxTypeAvcC())
		if _, err := mp4.Unmarshal(bytesReader(body[5:]), uint64(len(body[5:])), cfg, mp4.Context{}); err != nil {
			return nil, fmt.Errorf("parse H264 config: %w", err)
		}
		msg.AVCConfig = cfg
	} else if msg.Type == message.VideoTypeAU {
		msg.AU = body[5:]
	}
	return msg, nil
}

func bytesReader(buf []byte) io.ReadSeeker {
	return &byteReadSeeker{buf: buf}
}

type byteReadSeeker struct {
	buf []byte
	pos int64
}

func (r *byteReadSeeker) Read(p []byte) (int, error) {
	if r.pos >= int64(len(r.buf)) {
		return 0, io.EOF
	}
	n := copy(p, r.buf[r.pos:])
	r.pos += int64(n)
	return n, nil
}

func (r *byteReadSeeker) Seek(offset int64, whence int) (int64, error) {
	var next int64
	switch whence {
	case io.SeekStart:
		next = offset
	case io.SeekCurrent:
		next = r.pos + offset
	case io.SeekEnd:
		next = int64(len(r.buf)) + offset
	default:
		return 0, fmt.Errorf("invalid seek whence")
	}
	if next < 0 {
		return 0, fmt.Errorf("negative seek")
	}
	r.pos = next
	return r.pos, nil
}

func setMessageDTS(msg message.Message, dts time.Duration) message.Message {
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

func sleepUntil(ctx context.Context, target time.Time) error {
	for {
		wait := time.Until(target)
		if wait <= 0 {
			return nil
		}
		if wait > 100*time.Millisecond {
			wait = 100 * time.Millisecond
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
