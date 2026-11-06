//go:build integration

package media

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/megakuul/voiper/pkg/sip"
)

func TestBaresipLongCall(t *testing.T) {
	setting := os.Getenv("VOIPER_SOAK_DURATION")
	if setting == "" {
		t.Skip("set VOIPER_SOAK_DURATION=1h for the optional long-call check")
	}
	duration, err := time.ParseDuration(setting)
	if err != nil || duration < 10*time.Second || duration > 4*time.Hour {
		t.Fatal("VOIPER_SOAK_DURATION must be between 10s and 4h")
	}
	executable, err := exec.LookPath("baresip")
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	modules := filepath.Join(filepath.Dir(filepath.Dir(executable)), "lib", "baresip", "modules")
	if configured := os.Getenv("BARESIP_MODULE_PATH"); configured != "" {
		modules = configured
	}
	dir := t.TempDir()
	socket, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := socket.LocalAddr().String()
	socket.Close()
	config := fmt.Sprintf(`sip_listen %s
sip_transports udp
net_interface 127.0.0.1
module_path %s
audio_player aubridge,discard
audio_source ausine,400
audio_alert aubridge,discard
ausrc_format s16
auplay_format s16
auenc_format s16
audec_format s16
audio_jitter_buffer_type fixed
audio_jitter_buffer_ms 40-80
snd_path %s
module stdio.so
module g711.so
module ausine.so
module aubridge.so
module sndfile.so
module_app account.so
module_app menu.so
`, address, modules, dir)
	for name, contents := range map[string]string{"config": config, "accounts": fmt.Sprintf("<sip:peer@%s>;regint=0;answermode=auto;audio_codecs=PCMA\n", address)} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), duration+45*time.Second)
	defer cancel()
	log, err := os.Create(filepath.Join(dir, "baresip.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	command := exec.CommandContext(ctx, executable, "-4", "-f", dir, "-c")
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	command.Stdout = log
	command.Stderr = log
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		command.Wait()
		if t.Failed() {
			log.Seek(-min(65536, soakFileSize(log)), io.SeekEnd)
			tail, _ := io.ReadAll(log)
			t.Logf("peer log tail:\n%s", tail)
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, _ := os.ReadFile(log.Name())
		if strings.Contains(string(data), "baresip is ready") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("baresip did not become ready")
		}
		time.Sleep(20 * time.Millisecond)
	}
	events := make(chan sip.Event, 64)
	client, err := sip.NewClient(sip.Config{Server: "127.0.0.1", Username: "soak", LocalAddress: "127.0.0.1:0", SessionExpires: 120 * time.Second, MinSessionExpires: 90 * time.Second}, func(event sip.Event) {
		select {
		case events <- event:
		case <-ctx.Done():
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	call, err := NewCall(ctx, "soak", Settings{BindAddress: "127.0.0.1:0", Codecs: []string{"PCMA"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer call.Close()
	call.openAudio = syntheticAudio
	client.SetAnswerCommitHandler(func(_ string, offer, answer []byte) error { return call.AcceptAnswer(offer, answer) })
	client.SetOfferHandler(func(_ string, offer []byte) ([]byte, error) {
		if len(offer) == 0 {
			return call.CurrentOffer("127.0.0.1"), nil
		}
		return call.AnswerOffer(offer, "127.0.0.1")
	})
	client.SetAnswerHandler(func(_ string, answer []byte) error { return call.ValidateAnswer(answer) })
	if err := client.DialID(ctx, "soak", "sip:peer@"+address, call.LocalSDP("127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	connected := false
	for !connected {
		select {
		case event := <-events:
			if event.Type == "ended" {
				t.Fatalf("call failed: %+v", event)
			}
			if event.Type == "connected" {
				if err := call.Connect(event.SDP); err != nil {
					t.Fatal(err)
				}
				connected = true
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	started := time.Now()
	t.Logf("long call started %s; duration %s; peer %s", started.UTC().Format(time.RFC3339), duration, address)
	frame := make([]byte, 320)
	for i := 0; i < 160; i++ {
		binary.LittleEndian.PutUint16(frame[2*i:], uint16(int16(10000*math.Sin(2*math.Pi*700*float64(i)/8000))))
	}
	output := make([]byte, 16000)
	window := make([]byte, 0, 80000)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	check := time.NewTicker(5 * time.Second)
	defer check.Stop()
	finish := time.NewTimer(duration)
	defer finish.Stop()
	var baseline runtime.MemStats
	baselineGoroutines, baselineFDs := 0, 0
	lastReport := time.Time{}
	var previousPackets uint64
	running := true
	for running {
		select {
		case event := <-events:
			if event.Type == "ended" || event.Type == "renegotiation-failed" {
				t.Fatalf("long call interrupted: %+v", event)
			}
		case <-ticker.C:
			call.stream.Capture.Write(frame)
			n := call.stream.Playback.Read(output)
			if len(window)+n > cap(window) {
				copy(window, window[n:])
				window = window[:len(window)-n]
			}
			window = append(window, output[:n]...)
		case <-check.C:
			stats := call.Stats()
			if !baresipHasTone(window, 8000, 400) || stats.PacketsReceived <= previousPackets {
				t.Fatalf("audio stopped at %s: %+v", time.Since(started), stats)
			}
			previousPackets = stats.PacketsReceived
			window = window[:0]
			if time.Since(lastReport) >= time.Minute {
				runtime.GC()
				var memory runtime.MemStats
				runtime.ReadMemStats(&memory)
				descriptors, err := os.ReadDir("/proc/self/fd")
				if err != nil {
					t.Fatal(err)
				}
				goroutines := runtime.NumGoroutine()
				if baselineGoroutines == 0 {
					baseline = memory
					baselineGoroutines = goroutines
					baselineFDs = len(descriptors)
				}
				if memory.HeapAlloc > baseline.HeapAlloc+(32<<20) || goroutines > baselineGoroutines+32 || len(descriptors) > baselineFDs+8 {
					t.Fatalf("resources grew beyond long-call budget: heap=%d goroutines=%d fds=%d", memory.HeapAlloc, goroutines, len(descriptors))
				}
				t.Logf("elapsed=%s packets=%d/%d lost=%d heap=%d goroutines=%d fds=%d", time.Since(started).Round(time.Second), stats.PacketsSent, stats.PacketsReceived, stats.PacketsLost, memory.HeapAlloc, goroutines, len(descriptors))
				lastReport = time.Now()
			}
		case <-finish.C:
			running = false
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if err := client.Hangup(ctx, "soak"); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for {
		data, _ := os.ReadFile(log.Name())
		if strings.Contains(string(data), "terminated (duration:") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("peer did not terminate the call")
		}
		time.Sleep(20 * time.Millisecond)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*-dec.wav"))
	if err != nil || len(files) == 0 {
		t.Fatal("missing peer decoded recording")
	}
	heard := false
	for _, path := range files {
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if soakFileSize(file) > 16044 {
			file.Seek(-16000, io.SeekEnd)
			pcm := make([]byte, 16000)
			_, err = io.ReadFull(file, pcm)
			if err == nil && baresipHasTone(pcm, 8000, 700) {
				heard = true
			}
		}
		file.Close()
	}
	if !heard {
		t.Fatal("peer had no decoded local tone at end of long call")
	}
	t.Logf("long call completed %s; elapsed %s", time.Now().UTC().Format(time.RFC3339), time.Since(started).Round(time.Second))
}

func soakFileSize(file *os.File) int64 {
	info, err := file.Stat()
	if err != nil {
		return 0
	}
	return info.Size()
}
