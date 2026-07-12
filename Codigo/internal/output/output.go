package output

import (
	"context"

	"github.com/bluenviron/gortmplib/pkg/message"
)

// Publisher republishes encoded RTMP messages to an output stream.
//
// Implementations must preserve packet payload, timing, and keyframe metadata.
type Publisher interface {
	Connect(ctx context.Context) error
	Publish(ctx context.Context, msg message.Message) error
	Close() error
	Stats() Stats
}

type Stats struct {
	Connected      bool
	AudioPackets   uint64
	VideoPackets   uint64
	BytesPublished uint64
}
