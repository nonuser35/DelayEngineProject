package media

import (
	"time"

	"github.com/bluenviron/gortmplib/pkg/message"
)

type PacketType string

const (
	PacketTypeAudio PacketType = "audio"
	PacketTypeVideo PacketType = "video"
	PacketTypeData  PacketType = "data"
)

// Packet represents encoded RTMP media data traveling through DelayEngine.
//
// RTMPMessage must remain encoded and unmodified throughout the pipeline.
type Packet struct {
	Type        PacketType
	Codec       string
	PTS         time.Duration
	DTS         time.Duration
	Duration    time.Duration
	IsKeyFrame  bool
	ReceivedAt  time.Time
	Data        []byte
	Parts       [][]byte
	RTMPMessage message.Message
}

func (p Packet) Clone() Packet {
	cloned := p
	if p.Data != nil {
		cloned.Data = append([]byte(nil), p.Data...)
	}
	if p.Parts != nil {
		cloned.Parts = make([][]byte, len(p.Parts))
		for i, part := range p.Parts {
			cloned.Parts[i] = append([]byte(nil), part...)
		}
	}
	return cloned
}

func (p Packet) Timestamp() time.Duration {
	if p.DTS != 0 {
		return p.DTS
	}
	return p.PTS
}

func (p Packet) Size() int {
	size := len(p.Data)
	for _, part := range p.Parts {
		size += len(part)
	}
	return size
}
