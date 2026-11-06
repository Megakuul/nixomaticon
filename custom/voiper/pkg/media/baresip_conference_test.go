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

func TestBaresipConference(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	peers := []*conferencePeer{startConferencePeer(t, ctx, 400), startConferencePeer(t, ctx, 1000)}
	calls := make([]*Call, 2)
	clients := make([]*sip.Client, 2)
	for i := range calls {
		clients[i], calls[i] = connectConferencePeer(t, ctx, peers[i], fmt.Sprintf("conference-%d", i))
		defer clients[i].Close()
		defer calls[i].Close()
	}
	frame := make([]byte, 320)
	for i := 0; i < 160; i++ {
		binary.LittleEndian.PutUint16(frame[2*i:], uint16(int16(8000*math.Sin(2*math.Pi*700*float64(i)/8000))))
	}
	exchange := func(duration time.Duration) {
		timer := time.NewTimer(duration)
		defer timer.Stop()
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-timer.C:
				return
			case <-ticker.C:
				for _, call := range calls {
					call.stream.Capture.Write(frame)
				}
			}
		}
	}
	exchange(2 * time.Second)
	for _, peer := range peers {
		pcm := peer.decodedTail(t)
		if !baresipHasTone(pcm, 8000, 700) || baresipHasTone(pcm, 8000, 400) || baresipHasTone(pcm, 8000, 1000) {
			t.Fatal("independent call did not contain only the local tone")
		}
	}
	if err := JoinConference(calls...); err != nil {
		t.Fatal(err)
	}
	exchange(3 * time.Second)
	for i, peer := range peers {
		pcm := peer.decodedTail(t)
		own, other := 400, 1000
		if i == 1 {
			own, other = other, own
		}
		if !baresipHasTone(pcm, 8000, 700) || !baresipHasTone(pcm, 8000, other) || baresipHasTone(pcm, 8000, own) {
			t.Fatalf("peer %d did not decode local + other participant without its own tone", i)
		}
	}
	for _, call := range calls {
		call.LeaveConference()
	}
	exchange(2 * time.Second)
	for i, peer := range peers {
		pcm := peer.decodedTail(t)
		if !baresipHasTone(pcm, 8000, 700) || baresipHasTone(pcm, 8000, 400) || baresipHasTone(pcm, 8000, 1000) {
			t.Fatalf("peer %d still heard conference audio after leaving", i)
		}
		if err := clients[i].Hangup(ctx, fmt.Sprintf("conference-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	t.Log("both independent peers decoded local + other participant audio with mix-minus; leaving restored separate calls")
}

func TestBaresipRepeatedCallCleanup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	peer := startConferencePeer(t, ctx, 400)
	var baseline runtime.MemStats
	var baselineGoroutines, baselineFDs int
	for cycle := 0; cycle < 30; cycle++ {
		id := fmt.Sprintf("cycle-%d", cycle)
		if !t.Run(id, func(t *testing.T) {
			client, call := connectConferencePeer(t, ctx, peer, id)
			deadline := time.NewTimer(2 * time.Second)
			defer deadline.Stop()
			ticker := time.NewTicker(20 * time.Millisecond)
			defer ticker.Stop()
			pcm := make([]byte, 0, 8000)
			buffer := make([]byte, 1600)
			for !baresipHasTone(pcm, 8000, 400) {
				select {
				case <-deadline.C:
					t.Fatalf("cycle %d did not decode peer audio", cycle)
				case <-ticker.C:
					if n := call.stream.Playback.Read(buffer); n > 0 {
						pcm = append(pcm, buffer[:n]...)
					}
				}
			}
			if err := client.Hangup(ctx, id); err != nil {
				t.Fatal(err)
			}
			if err := call.Close(); err != nil {
				t.Fatal(err)
			}
			if err := client.Close(); err != nil {
				t.Fatal(err)
			}
		}) {
			return
		}
		if cycle%5 == 4 {
			runtime.GC()
			var memory runtime.MemStats
			runtime.ReadMemStats(&memory)
			fds, err := os.ReadDir("/proc/self/fd")
			if err != nil {
				t.Fatal(err)
			}
			goroutines := runtime.NumGoroutine()
			if cycle == 4 {
				baseline, baselineGoroutines, baselineFDs = memory, goroutines, len(fds)
			} else if memory.HeapAlloc > baseline.HeapAlloc+(8<<20) || goroutines > baselineGoroutines+4 || len(fds) > baselineFDs+2 {
				t.Fatalf("resources grew after %d cycles: heap %d -> %d, goroutines %d -> %d, descriptors %d -> %d", cycle+1, baseline.HeapAlloc, memory.HeapAlloc, baselineGoroutines, goroutines, baselineFDs, len(fds))
			}
			t.Logf("closed %d separate clients/calls: heap=%d goroutines=%d descriptors=%d", cycle+1, memory.HeapAlloc, goroutines, len(fds))
		}
	}
	// UDP SIP transactions retain retransmission state for 64*T1 (32 seconds).
	// Measure again after that protocol grace period, not just after socket close.
	select {
	case <-time.After(33 * time.Second):
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	runtime.GC()
	var settled runtime.MemStats
	runtime.ReadMemStats(&settled)
	if settled.HeapAlloc > baseline.HeapAlloc+(1<<20) {
		t.Fatalf("heap remained elevated after transaction expiry: %d -> %d", baseline.HeapAlloc, settled.HeapAlloc)
	}
	t.Logf("after transaction expiry: heap=%d (warmup=%d)", settled.HeapAlloc, baseline.HeapAlloc)
}

func connectConferencePeer(t *testing.T, ctx context.Context, peer *conferencePeer, id string) (*sip.Client, *Call) {
	t.Helper()
	events := make(chan sip.Event, 32)
	client, err := sip.NewClient(sip.Config{Server: "127.0.0.1", Username: "conference", LocalAddress: "127.0.0.1:0"}, func(event sip.Event) {
		select {
		case events <- event:
		case <-ctx.Done():
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	call, err := NewCall(ctx, id, Settings{BindAddress: "127.0.0.1:0", Codecs: []string{"PCMA"}}, nil)
	if err != nil {
		client.Close()
		t.Fatal(err)
	}
	// Each cycle closes these immediately; cleanup also covers a failed assertion.
	t.Cleanup(func() { call.Close(); client.Close() })
	call.openAudio = syntheticAudio
	client.SetAnswerCommitHandler(func(_ string, offer, answer []byte) error { return call.AcceptAnswer(offer, answer) })
	if err := client.DialID(ctx, id, "sip:peer@"+peer.address, call.LocalSDP("127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case event := <-events:
			if event.Type == "ended" {
				t.Fatalf("call ended: %+v", event)
			}
			if event.Type == "connected" {
				if err := call.Connect(event.SDP); err != nil {
					t.Fatal(err)
				}
				return client, call
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

type conferencePeer struct{ address, dir string }

func startConferencePeer(t *testing.T, ctx context.Context, frequency int) *conferencePeer {
	t.Helper()
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
	socket, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	peer := &conferencePeer{address: socket.LocalAddr().String(), dir: t.TempDir()}
	socket.Close()
	config := fmt.Sprintf(`sip_listen %s
sip_transports udp
net_interface 127.0.0.1
module_path %s
audio_source ausine,%d
audio_player aubridge,discard
audio_alert aubridge,discard
ausrc_format s16
auplay_format s16
auenc_format s16
audec_format s16
snd_path %s
module stdio.so
module g711.so
module ausine.so
module aubridge.so
module sndfile.so
module_app account.so
module_app menu.so
`, peer.address, modules, frequency, peer.dir)
	for name, body := range map[string]string{"config": config, "accounts": fmt.Sprintf("<sip:peer@%s>;regint=0;answermode=auto;audio_codecs=PCMA\n", peer.address)} {
		if err := os.WriteFile(filepath.Join(peer.dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	log, err := os.Create(filepath.Join(peer.dir, "baresip.log"))
	if err != nil {
		t.Fatal(err)
	}
	processCtx, cancel := context.WithCancel(ctx)
	command := exec.CommandContext(processCtx, executable, "-4", "-f", peer.dir, "-c")
	input, err := command.StdinPipe()
	if err != nil {
		cancel()
		log.Close()
		t.Fatal(err)
	}
	command.Stdout, command.Stderr = log, log
	if err := command.Start(); err != nil {
		cancel()
		input.Close()
		log.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		input.Close()
		command.Wait()
		log.Close()
		if t.Failed() {
			data, _ := os.ReadFile(log.Name())
			t.Logf("peer log:\n%s", data)
		}
	})
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, _ := os.ReadFile(log.Name())
		if strings.Contains(string(data), "baresip is ready") {
			return peer
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-deadline.C:
			t.Fatal("peer did not become ready")
		case <-ticker.C:
		}
	}
}

func (peer *conferencePeer) decodedTail(t *testing.T) []byte {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(peer.dir, "*-dec.wav"))
	if err != nil || len(files) != 1 {
		t.Fatalf("expected one decoded WAV: %v / %v", files, err)
	}
	file, err := os.Open(files[0])
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Seek(-16000, io.SeekEnd); err != nil {
		t.Fatal(err)
	}
	pcm, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	return pcm
}
