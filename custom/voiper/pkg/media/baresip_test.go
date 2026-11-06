//go:build integration

package media

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/megakuul/voiper/pkg/sip"
)

func TestBaresipIndependentPeer(t *testing.T) {
	executable, err := exec.LookPath("baresip")
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"PCMU", "PCMA", "G722", "opus"} {
		for _, delayed := range []bool{false, true} {
			mode := "initial-offer"
			if delayed {
				mode = "delayed-offer"
			}
			t.Run(name+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
				defer cancel()
				dir := t.TempDir()
				socket, err := net.ListenPacket("udp4", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				address := socket.LocalAddr().String()
				socket.Close()
				modules := filepath.Join(filepath.Dir(filepath.Dir(executable)), "lib", "baresip", "modules")
				if configured := os.Getenv("BARESIP_MODULE_PATH"); configured != "" {
					modules = configured
				}
				for _, module := range []string{"g711", "g722", "opus", "stdio", "ausine", "aubridge", "sndfile", "account", "menu"} {
					if _, err := os.Stat(filepath.Join(modules, module+".so")); err != nil {
						t.Fatalf("required baresip module %s: %v", module, err)
					}
				}
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
rtp_stats yes
snd_path %s
module stdio.so
module g711.so
module g722.so
module opus.so
module ausine.so
module aubridge.so
module sndfile.so
module_app account.so
module_app menu.so
`, address, modules, dir)
				if err := os.WriteFile(filepath.Join(dir, "config"), []byte(config), 0600); err != nil {
					t.Fatal(err)
				}
				peerCodec := name
				if name == "opus" {
					peerCodec = "opus/48000/2"
				}
				account := fmt.Sprintf("<sip:peer@%s>;regint=0;answermode=auto;audio_codecs=%s\n", address, peerCodec)
				if err := os.WriteFile(filepath.Join(dir, "accounts"), []byte(account), 0600); err != nil {
					t.Fatal(err)
				}
				log, err := os.Create(filepath.Join(dir, "baresip.log"))
				if err != nil {
					t.Fatal(err)
				}
				defer log.Close()
				peerContext, stopPeer := context.WithCancel(ctx)
				defer stopPeer()
				command := exec.CommandContext(peerContext, executable, "-4", "-f", dir, "-c", "-s")
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
					stopPeer()
					input.Close()
					command.Wait()
					if t.Failed() {
						data, _ := os.ReadFile(log.Name())
						t.Logf("baresip log:\n%s", data)
					}
				}()
				awaitLog := func(text string) {
					t.Helper()
					deadline := time.Now().Add(5 * time.Second)
					for time.Now().Before(deadline) {
						data, _ := os.ReadFile(log.Name())
						if strings.Contains(string(data), text) {
							return
						}
						time.Sleep(20 * time.Millisecond)
					}
					t.Fatalf("missing peer evidence %q", text)
				}
				awaitLog("baresip is ready")
				events := make(chan sip.Event, 64)
				client, err := sip.NewClient(sip.Config{Server: "127.0.0.1", Username: "voiper", LocalAddress: "127.0.0.1:0"}, func(event sip.Event) {
					select {
					case events <- event:
					case <-ctx.Done():
					}
				})
				if err != nil {
					t.Fatal(err)
				}
				defer client.Close()
				var call *Call
				prepared := make(chan *Call, 1)
				settings := Settings{BindAddress: "127.0.0.1:0", Codecs: []string{name}}
				defer func() {
					cancel()
					if call != nil {
						call.Close()
					}
				}()
				next := func(kind string) sip.Event {
					t.Helper()
					for {
						select {
						case event := <-events:
							if event.Type == kind {
								return event
							}
							if event.Type == "failed" || event.Type == "ended" {
								t.Fatalf("SIP failure: %+v", event)
							}
						case <-ctx.Done():
							t.Fatalf("waiting for %s: %v", kind, ctx.Err())
							return sip.Event{}
						}
					}
				}
				if delayed {
					err = client.DialDelayedID(ctx, "independent", "sip:peer@"+address, func(answerCtx context.Context, offer []byte) ([]byte, error) {
						stream, err := NewCall(answerCtx, "independent", settings, offer)
						if err != nil {
							return nil, err
						}
						stream.openAudio = syntheticAudio
						prepared <- stream
						return stream.LocalSDP("127.0.0.1"), nil
					})
				} else {
					call, err = NewCall(ctx, "independent", settings, nil)
					if err != nil {
						t.Fatal(err)
					}
					call.openAudio = syntheticAudio
					err = client.DialID(ctx, "independent", "sip:peer@"+address, call.LocalSDP("127.0.0.1"))
				}
				if err != nil {
					t.Fatal(err)
				}
				connected := next("connected")
				if connected.DelayedOffer != delayed {
					t.Fatal("connected event reported the wrong offer mode")
				}
				if delayed {
					select {
					case call = <-prepared:
					default:
						t.Fatal("connected arrived before preparing the ACK answer")
					}
				}
				client.SetAnswerCommitHandler(func(_ string, offer, answer []byte) error { return call.AcceptAnswer(offer, answer) })
				if err := call.Connect(connected.SDP); err != nil {
					t.Fatal(err)
				}
				if !strings.EqualFold(call.Stats().Codec, name) {
					t.Fatalf("negotiated %s, wanted %s", call.Stats().Codec, name)
				}
				rate := call.Stats().DeviceSampleRate
				frame := make([]byte, rate/50*2)
				for i := 0; i < len(frame)/2; i++ {
					sample := int16(10000 * math.Sin(2*math.Pi*700*float64(i)/float64(rate)))
					binary.LittleEndian.PutUint16(frame[i*2:], uint16(sample))
				}
				exchange := func(duration time.Duration) {
					t.Helper()
					timer := time.NewTimer(duration)
					defer timer.Stop()
					ticker := time.NewTicker(20 * time.Millisecond)
					defer ticker.Stop()
					var received []byte
					output := make([]byte, rate*2)
					for {
						select {
						case <-timer.C:
							if !baresipHasTone(received, rate, 400) {
								t.Fatalf("no decoded 400 Hz peer tone in %d bytes", len(received))
							}
							return
						case <-ticker.C:
							call.stream.Capture.Write(frame)
							n := call.stream.Playback.Read(output)
							received = append(received, output[:n]...)
						case <-ctx.Done():
							t.Fatal(ctx.Err())
						}
					}
				}
				exchange(time.Second)
				digitEvidence := "received in-band DTMF event: '5' (end=1)"
				if err := call.SendDTMF("5"); err != nil {
					if name != "opus" || !errors.Is(err, ErrTelephoneEventsNotNegotiated) {
						t.Fatal(err)
					}
					if err := client.SendDTMF(ctx, "independent", "5"); err != nil {
						t.Fatal(err)
					}
					digitEvidence = "received SIP INFO DTMF: '5' (duration=160)"
				}
				exchange(500 * time.Millisecond)
				awaitLog(digitEvidence)
				if err := client.ReinviteWithOffer(ctx, "independent", func() ([]byte, error) { return call.LocalOffer("127.0.0.1", true), nil }); err != nil {
					t.Fatal(err)
				}
				if !call.Stats().Held {
					t.Fatal("hold was not committed")
				}
				time.Sleep(200 * time.Millisecond)
				heldPackets := call.Stats().PacketsReceived
				time.Sleep(200 * time.Millisecond)
				if call.Stats().PacketsReceived > heldPackets+1 {
					t.Fatal("peer continued transmitting audio while held")
				}
				if err := client.ReinviteWithOffer(ctx, "independent", func() ([]byte, error) { return call.LocalOffer("127.0.0.1", false), nil }); err != nil {
					t.Fatal(err)
				}
				if call.Stats().Held {
					t.Fatal("resume was not committed")
				}
				exchange(time.Second)
				if err := client.Hangup(ctx, "independent"); err != nil {
					t.Fatal(err)
				}
				next("ended")
				awaitLog("terminated (duration:")
				files, err := filepath.Glob(filepath.Join(dir, "*-dec.wav"))
				if err != nil || len(files) == 0 {
					t.Fatalf("missing peer WAV: %v", err)
				}
				heard := false
				for _, path := range files {
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					pcm, wavRate := baresipReadPCM(t, data)
					if baresipHasTone(pcm, wavRate, 700) {
						heard = true
					}
				}
				if !heard {
					t.Fatal("baresip did not decode Voiper's 700 Hz tone")
				}
				t.Logf("%s: decoded 400 Hz at Voiper and 700 Hz at baresip; hold/resume and BYE completed", name)
			})
		}
	}
}

func baresipHasTone(pcm []byte, rate, frequency int) bool {
	if rate == 0 || len(pcm) < rate/5*2 {
		return false
	}
	var real, imag, energy float64
	for i := 0; i < len(pcm)/2; i++ {
		sample := float64(int16(binary.LittleEndian.Uint16(pcm[2*i:])))
		angle := 2 * math.Pi * float64(frequency*i) / float64(rate)
		real += sample * math.Cos(angle)
		imag += sample * math.Sin(angle)
		energy += sample * sample
	}
	return energy > 0 && (real*real+imag*imag)/(float64(len(pcm)/2)*energy) > 0.08
}
func baresipReadPCM(t *testing.T, data []byte) ([]byte, int) {
	t.Helper()
	if len(data) < 12 || string(data[:4]) != "RIFF" {
		t.Fatal("invalid WAV")
	}
	rate, channels := 0, 0
	for offset := 12; offset+8 <= len(data); {
		size := int(binary.LittleEndian.Uint32(data[offset+4:]))
		end := offset + 8 + size
		if end > len(data) {
			t.Fatal("truncated WAV")
		}
		switch string(data[offset : offset+4]) {
		case "fmt ":
			if size < 16 || binary.LittleEndian.Uint16(data[offset+8:]) != 1 || binary.LittleEndian.Uint16(data[offset+22:]) != 16 {
				t.Fatal("WAV is not PCM16")
			}
			rate = int(binary.LittleEndian.Uint32(data[offset+12:]))
			channels = int(binary.LittleEndian.Uint16(data[offset+10:]))
		case "data":
			if channels < 1 {
				t.Fatal("missing WAV channels")
			}
			pcm := make([]byte, 0, size/channels)
			for i := offset + 8; i+channels*2 <= end; i += channels * 2 {
				pcm = append(pcm, data[i:i+2]...)
			}
			return pcm, rate
		}
		offset = end + size%2
	}
	t.Fatal("missing PCM")
	return nil, 0
}
