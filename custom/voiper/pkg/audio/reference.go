package audio

import (
	"errors"
	"sync"
)

type referenceTaps struct{ queues []*Ring }

// TapPlayback provides a private copy of frames actually submitted to playback.
// Registration can block briefly; the device callback only loads a fixed snapshot
// and writes bounded queues. Each tap has exactly one consumer.
func (s *Stream) TapPlayback(capacity int) (*Ring, func(), error) {
	if capacity < 2 || capacity > 96000 || capacity%2 != 0 {
		return nil, nil, errors.New("invalid playback reference capacity")
	}
	s.tapsMu.Lock()
	defer s.tapsMu.Unlock()
	if s.stopped.Load() {
		return nil, nil, errors.New("playback device is stopped")
	}
	current := s.taps.Load()
	queues := []*Ring{}
	if current != nil {
		queues = append(queues, current.queues...)
	}
	if len(queues) >= 8 {
		return nil, nil, errors.New("playback reference tap limit reached")
	}
	queue := NewRing(capacity)
	s.taps.Store(&referenceTaps{queues: append(queues, queue)})
	var once sync.Once
	stop := func() {
		once.Do(func() {
			s.tapsMu.Lock()
			defer s.tapsMu.Unlock()
			current := s.taps.Load()
			if current == nil {
				return
			}
			remaining := make([]*Ring, 0, len(current.queues)-1)
			for _, candidate := range current.queues {
				if candidate != queue {
					remaining = append(remaining, candidate)
				}
			}
			s.taps.Store(&referenceTaps{queues: remaining})
		})
	}
	return queue, stop, nil
}
