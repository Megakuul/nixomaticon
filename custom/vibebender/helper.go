package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Wire protocol between the GUI (user) and the helper (root), one JSON object
// per line. The password only ever travels over the helper's stdin.

// command is sent GUI -> helper.
type command struct {
	Cmd    string  `json:"cmd"` // "connect", "respond", "disconnect"
	Config *Config `json:"config,omitempty"`
	Value  string  `json:"value,omitempty"`
}

// event is sent helper -> GUI.
type event struct {
	Event   string   `json:"event"`           // "log", "state", "prompt", "connected", "exit"
	Retry   int      `json:"retry,omitempty"` // seconds until the next attempt ("reconnecting")
	Level   string   `json:"level,omitempty"`
	Msg     string   `json:"msg,omitempty"`
	Time    string   `json:"time,omitempty"`
	State   string   `json:"state,omitempty"`
	Error   string   `json:"error,omitempty"`
	Device  string   `json:"device,omitempty"`
	Gateway string   `json:"gateway,omitempty"`
	Routes  []string `json:"routes,omitempty"`
	DNS     []string `json:"dns,omitempty"`
	Code    int      `json:"code,omitempty"` // "exit" (GUI side only)
}

var (
	emitMu  sync.Mutex
	emitEnc = json.NewEncoder(os.Stdout)
)

func emit(e event) {
	emitMu.Lock()
	defer emitMu.Unlock()
	emitEnc.Encode(e)
}

func emitState(state string, err error) {
	e := event{Event: "state", State: state}
	if err != nil {
		e.Error = err.Error()
	}
	emit(e)
}

// sessionRunner runs one VPN session until it ends or ctx is cancelled.
type sessionRunner func(ctx context.Context, cfg *Config, prompt PromptFunc, onUp ConnectedFunc) error

func realSession(ctx context.Context, cfg *Config, prompt PromptFunc, onUp ConnectedFunc) error {
	return NewNXSession(cfg, prompt, onUp).Run(ctx)
}

// A session that stayed up this long resets the reconnect backoff.
const stableAfter = 2 * time.Minute

// reconnectDelay is the wait before reconnect attempt n (1-based):
// 1s, 2s, 4s, ... capped at 60s.
func reconnectDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 7 {
		return 60 * time.Second
	}
	return min(time.Duration(1<<(attempt-1))*time.Second, 60*time.Second)
}

// runHelper drives the VPN until a "disconnect" command or stdin closes (the
// GUI went away). With autoReconnect it re-establishes dropped sessions,
// except after the server rejected the login.
func runHelper(fake bool) int {
	return serveHelper(os.Stdin, os.Stdout, fake)
}

// serveHelper is runHelper over arbitrary streams (tests drive it directly).
func serveHelper(in io.Reader, out io.Writer, fake bool) int {
	emitMu.Lock()
	emitEnc = json.NewEncoder(out)
	emitMu.Unlock()
	slog.SetDefault(slog.New(&eventHandler{level: slog.LevelDebug}))

	run := realSession
	if fake {
		run = fakeSession
	} else if os.Geteuid() != 0 {
		emitState("error", errors.New("helper must run as root"))
		return 1
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	responses := make(chan string)
	configs := make(chan *Config, 1)

	go func() {
		scanner := bufio.NewScanner(in)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			var c command
			if err := json.Unmarshal(scanner.Bytes(), &c); err != nil {
				slog.Warn("invalid command", "error", err)
				continue
			}
			switch c.Cmd {
			case "connect":
				if c.Config != nil {
					select {
					case configs <- c.Config:
					default:
					}
				}
			case "respond":
				select {
				case responses <- c.Value:
				case <-ctx.Done():
				}
			case "disconnect":
				cancel()
			}
		}
		// GUI closed its end: tear everything down.
		cancel()
	}()

	var cfg *Config
	select {
	case cfg = <-configs:
	case <-ctx.Done():
		emitState("disconnected", nil)
		return 0
	}
	if cfg.Port == 0 {
		cfg.Port = 443
	}
	if cfg.MaxLine == 0 {
		cfg.MaxLine = 1500
	}

	prompt := func(ctx context.Context, message string) (string, error) {
		emit(event{Event: "prompt", Msg: strings.TrimSpace(message)})
		select {
		case r := <-responses:
			return r, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	go func() {
		<-ctx.Done()
		emitState("disconnecting", nil)
	}()

	attempt := 0
	for {
		emitState("connecting", nil)
		var upAt atomic.Int64
		err := run(ctx, cfg, prompt, func(device, gateway string, routes, dns []string) {
			upAt.Store(time.Now().UnixNano())
			emit(event{Event: "connected", Device: device, Gateway: gateway, Routes: routes, DNS: dns})
			emitState("connected", nil)
		})
		if ctx.Err() != nil {
			emitState("disconnected", nil)
			return 0
		}
		if err == nil {
			err = errors.New("session ended")
		}
		slog.Error("session failed", "error", err)

		var authErr *authError
		if !cfg.AutoReconnect || errors.As(err, &authErr) {
			emitState("error", err)
			return 1
		}

		if up := upAt.Load(); up != 0 && time.Since(time.Unix(0, up)) > stableAfter {
			attempt = 0
		}
		attempt++
		delay := reconnectDelay(attempt)
		slog.Info("reconnecting", "in", delay, "attempt", attempt)
		emit(event{Event: "state", State: "reconnecting", Error: err.Error(), Retry: int(delay.Seconds())})
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			emitState("disconnected", nil)
			return 0
		}
	}
}

// eventHandler turns slog records into "log" events for the GUI console.
type eventHandler struct {
	level slog.Level
	attrs []slog.Attr
}

func (h *eventHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }

func (h *eventHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Message)
	write := func(a slog.Attr) bool {
		fmt.Fprintf(&b, " %s=%v", a.Key, a.Value.Any())
		return true
	}
	for _, a := range h.attrs {
		write(a)
	}
	r.Attrs(write)
	emit(event{
		Event: "log",
		Level: strings.ToLower(r.Level.String()),
		Msg:   b.String(),
		Time:  r.Time.Format(time.TimeOnly),
	})
	return nil
}

func (h *eventHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &eventHandler{level: h.level, attrs: append(append([]slog.Attr{}, h.attrs...), attrs...)}
}

func (h *eventHandler) WithGroup(string) slog.Handler { return h }
