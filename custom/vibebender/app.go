package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is bound to the frontend. It owns the privileged helper process and
// relays its events to the UI as "vb:event".
type App struct {
	ctx context.Context

	mu      sync.Mutex
	stdin   io.WriteCloser
	done    chan struct{}
	closing bool
}

func NewApp() *App { return &App{} }

func (a *App) startup(ctx context.Context) { a.ctx = ctx }

// LoadConfig returns the saved profile (without password).
func (a *App) LoadConfig() Config { return loadConfig() }

// Connect saves the profile and starts a privileged helper session.
func (a *App) Connect(cfg Config) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.done != nil {
		return errors.New("a session is already running")
	}
	if cfg.Server == "" || cfg.Username == "" || cfg.Password == "" {
		return errors.New("server, username and password are required")
	}
	if err := saveConfig(cfg); err != nil {
		a.emit(event{Event: "log", Level: "warn", Msg: "could not save profile: " + err.Error()})
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	var cmd *exec.Cmd
	switch {
	case os.Getenv("VIBEBENDER_FAKE") != "":
		// UI development: simulated VPN, no root, no network.
		cmd = exec.Command(exe, "helper", "--fake")
	case os.Geteuid() == 0:
		cmd = exec.Command(exe, "helper")
	default:
		cmd = exec.Command("pkexec", exe, "helper")
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start helper: %w", err)
	}

	a.stdin = stdin
	done := make(chan struct{})
	a.done = done

	// Sent before pkexec has authenticated; it simply waits in the pipe.
	if err := json.NewEncoder(stdin).Encode(command{Cmd: "connect", Config: &cfg}); err != nil {
		return err
	}

	go a.relay(stdout)
	// The last stderr line explains a pkexec failure (e.g. "No authentication
	// agent found"), so it is passed along with the exit event.
	var lastErr string
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		s := bufio.NewScanner(stderr)
		for s.Scan() {
			lastErr = s.Text()
			a.emit(event{Event: "log", Level: "warn", Msg: lastErr, Time: time.Now().Format(time.TimeOnly)})
		}
	}()
	go func() {
		<-stderrDone
		err := cmd.Wait()
		code := 0
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		}
		a.mu.Lock()
		a.stdin, a.done = nil, nil
		a.mu.Unlock()
		close(done)
		// pkexec: 126 = authorization dismissed/denied, 127 = not authorized.
		a.emit(event{Event: "exit", Code: code, Error: lastErr})
	}()
	return nil
}

func (a *App) relay(r io.Reader) {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 64*1024), 1024*1024)
	for s.Scan() {
		var e event
		if json.Unmarshal(s.Bytes(), &e) == nil {
			a.emit(e)
		}
	}
}

// Respond answers a 2FA prompt.
func (a *App) Respond(value string) error {
	return a.send(command{Cmd: "respond", Value: value})
}

// Disconnect asks the helper to tear the tunnel down.
func (a *App) Disconnect() error {
	return a.send(command{Cmd: "disconnect"})
}

func (a *App) send(c command) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stdin == nil {
		return errors.New("not connected")
	}
	return json.NewEncoder(a.stdin).Encode(c)
}

func (a *App) emit(e event) {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "vb:event", e)
	}
}

// beforeClose disconnects cleanly before the window goes away.
func (a *App) beforeClose(ctx context.Context) bool {
	a.mu.Lock()
	done := a.done
	if done == nil || a.closing {
		a.mu.Unlock()
		return false
	}
	a.closing = true
	a.mu.Unlock()

	go func() {
		a.Disconnect()
		select {
		case <-done:
		case <-time.After(8 * time.Second):
		}
		runtime.Quit(ctx)
	}()
	return true
}
