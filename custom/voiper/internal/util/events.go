package util

import (
	"context"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// EventWriter wraps the wails event writer in a io.Writer().
type EventWriter struct {
	ctx   context.Context // ctx must be derived from the wails app ctx
	event string
}

func NewEventWriter(ctx context.Context, event string) *EventWriter {
	return &EventWriter{
		ctx:   ctx,
		event: event,
	}
}

func (e EventWriter) Write(data []byte) (int, error) {
	runtime.EventsEmit(e.ctx, e.event, string(data))
	return len(data), nil
}
