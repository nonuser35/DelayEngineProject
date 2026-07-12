package delay

import (
	"sync/atomic"
	"time"
)

type State struct {
	enabled atomic.Bool
	delayNS atomic.Int64
	version atomic.Uint64
}

func NewState(enabled bool, delay time.Duration) *State {
	state := &State{}
	state.enabled.Store(enabled)
	state.SetDelay(delay)
	return state
}

func (s *State) Enabled() bool {
	return s.enabled.Load()
}

func (s *State) Enable() {
	if !s.enabled.Swap(true) {
		s.version.Add(1)
	}
}

func (s *State) Disable() {
	if s.enabled.Swap(false) {
		s.version.Add(1)
	}
}

func (s *State) Delay() time.Duration {
	return time.Duration(s.delayNS.Load())
}

func (s *State) SetDelay(delay time.Duration) {
	if delay < 0 {
		delay = 0
	}
	if s.delayNS.Swap(int64(delay)) != int64(delay) {
		s.version.Add(1)
	}
}

func (s *State) Version() uint64 {
	return s.version.Load()
}
