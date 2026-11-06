package audio

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Keep one diagnostic operation until its native resources finish closing, even
// if a stalled sound server outlives the caller's cancellation deadline.
var diagnosticDevice = make(chan struct{}, 1)

func openDiagnostic(ctx context.Context, opener func() (*Stream, error)) (*Stream, func(), error) {
	select {
	case diagnosticDevice <- struct{}{}:
	default:
		return nil, nil, errors.New("another audio test or tone is still active")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	type result struct {
		stream *Stream
		err    error
	}
	ready := make(chan result)
	go func() {
		stream, err := opener()
		select {
		case ready <- result{stream, err}:
		case <-ctx.Done():
			if stream != nil {
				stream.Close()
			}
			<-diagnosticDevice
		}
	}()
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case opened := <-ready:
		if opened.err != nil {
			<-diagnosticDevice
			return nil, nil, opened.err
		}
		var once sync.Once
		closed := make(chan struct{})
		closeDevice := func() {
			once.Do(func() { go func() { opened.stream.Close(); <-diagnosticDevice; close(closed) }() })
			timer := time.NewTimer(2 * time.Second)
			defer timer.Stop()
			select {
			case <-closed:
			case <-timer.C:
			}
		}
		if ctx.Err() != nil {
			closeDevice()
			return nil, nil, ctx.Err()
		}
		return opened.stream, closeDevice, nil
	}
}
