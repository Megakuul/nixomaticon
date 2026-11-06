//go:build integration

package baresip_test

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/megakuul/voiper/pkg/sip"
)

// These are signaling checks. The media package's independent-peer fixtures
// separately verify decoded audio; the SDP here intentionally disables audio.
const inactiveSDP = "v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=Voiper signaling acceptance\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio 40000 RTP/AVP 8\r\na=rtpmap:8 PCMA/8000\r\na=inactive\r\n"

func TestBaresipMessagesAndPresence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, events := newClient(t, ctx)
	peer := startPeer(t, ctx, "bob", "sip:voiper@"+client.LocalAddr())
	if err := client.SendMessage(ctx, peer.uri, "hello independent peer"); err != nil {
		t.Fatal(err)
	}
	peer.waitLog(t, ctx, `"hello independent peer"`)
	peer.command(t, "/message hello Voiper")
	event := waitEvent(t, ctx, events, "message", "")
	if event.Message != "hello Voiper" || event.RemoteURI != peer.uri || event.ContentType != "text/plain" {
		t.Fatalf("incorrect received message: %+v", event)
	}

	peer.command(t, "/presence online")
	peer.waitLog(t, ctx, "presence: update status")
	id, err := client.Subscribe(ctx, peer.uri, "presence", "application/pidf+xml")
	if err != nil {
		t.Fatal(err)
	}
	assertPresence(t, waitEvent(t, ctx, events, "presence", id), peer.uri, "open")
	peer.command(t, "/presence offline")
	assertPresence(t, waitEvent(t, ctx, events, "presence", id), peer.uri, "closed")
	if err := client.Unsubscribe(ctx, id); err != nil {
		t.Fatal(err)
	}
}

func TestBaresipIncomingCall(t *testing.T) {
	for _, action := range []string{"answer", "reject", "cancel"} {
		t.Run(action, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			client, events := newClient(t, ctx)
			peer := startPeer(t, ctx, "bob", "")
			peer.command(t, "/dial sip:voiper@"+client.LocalAddr())
			incoming := waitEvent(t, ctx, events, "incoming", "")
			if incoming.RemoteURI != peer.uri || len(incoming.SDP) == 0 {
				t.Fatalf("incorrect incoming call: %+v", incoming)
			}
			switch action {
			case "answer":
				if err := client.Answer(ctx, incoming.CallID, []byte(inactiveSDP)); err != nil {
					t.Fatal(err)
				}
				waitEvent(t, ctx, events, "connected", incoming.CallID)
				peer.waitLog(t, ctx, "Call established:")
				peer.command(t, "/hangup")
			case "reject":
				if err := client.Reject(ctx, incoming.CallID); err != nil {
					t.Fatal(err)
				}
				peer.waitLog(t, ctx, "486 Busy Here")
			case "cancel":
				peer.command(t, "/hangup")
			}
			waitEvent(t, ctx, events, "ended", incoming.CallID)
		})
	}
}

func TestBaresipTransfer(t *testing.T) {
	for _, attended := range []bool{false, true} {
		name := "blind"
		if attended {
			name = "attended"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			client, events := newClient(t, ctx)
			bob := startPeer(t, ctx, "bob", "")
			carol := startPeer(t, ctx, "carol", "")
			original, err := client.Dial(ctx, bob.uri, []byte(inactiveSDP))
			if err != nil {
				t.Fatal(err)
			}
			waitEvent(t, ctx, events, "connected", original)
			if attended {
				consultation, err := client.Dial(ctx, carol.uri, []byte(inactiveSDP))
				if err != nil {
					t.Fatal(err)
				}
				waitEvent(t, ctx, events, "connected", consultation)
				if err := client.TransferToCall(ctx, original, consultation); err != nil {
					t.Fatal(err)
				}
			} else if err := client.Transfer(ctx, original, carol.uri); err != nil {
				t.Fatal(err)
			}
			for {
				event := waitEvent(t, ctx, events, "transfer", original)
				if strings.Contains(string(event.Body), "200 OK") {
					break
				}
				if !strings.Contains(string(event.Body), "100 Trying") {
					t.Fatalf("unexpected transfer result: %+v", event)
				}
			}
			carol.waitLog(t, ctx, "Call established: "+bob.uri)
			bob.waitLog(t, ctx, "Call established: "+carol.uri)
			// The transferor releases its original leg only after final NOTIFY.
			if err := client.Hangup(ctx, original); err != nil {
				t.Fatal(err)
			}
			waitEvent(t, ctx, events, "ended", original)
			carol.command(t, "/hangup")
			bob.waitLog(t, ctx, carol.uri+": session closed:")
		})
	}
}

func assertPresence(t *testing.T, event sip.Event, entity, basic string) {
	t.Helper()
	var document struct {
		XMLName xml.Name `xml:"urn:ietf:params:xml:ns:pidf presence"`
		Entity  string   `xml:"entity,attr"`
		Basic   string   `xml:"tuple>status>basic"`
	}
	if err := xml.Unmarshal(event.Body, &document); err != nil {
		t.Fatal(err)
	}
	if document.Entity != entity || document.Basic != basic || event.ContentType != "application/pidf+xml" {
		t.Fatalf("incorrect presence: %+v / %+v", event, document)
	}
}

func newClient(t *testing.T, ctx context.Context) (*sip.Client, <-chan sip.Event) {
	t.Helper()
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
	t.Cleanup(func() { client.Close() })
	return client, events
}

func waitEvent(t *testing.T, ctx context.Context, events <-chan sip.Event, kind, id string) sip.Event {
	t.Helper()
	for {
		select {
		case event := <-events:
			if event.Type == kind && (id == "" || event.CallID == id) {
				return event
			}
			if event.Type == "ended" && event.CallID == id && kind == "connected" {
				t.Fatalf("call ended before connecting: %+v", event)
			}
		case <-ctx.Done():
			t.Fatalf("waiting for %s (%s): %v", kind, id, ctx.Err())
		}
	}
}

type peer struct {
	uri   string
	input io.WriteCloser
	log   string
}

func startPeer(t *testing.T, ctx context.Context, user, contact string) *peer {
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
	address := socket.LocalAddr().String()
	socket.Close()
	dir := t.TempDir()
	p := &peer{uri: "sip:" + user + "@" + address, log: filepath.Join(dir, "baresip.log")}
	config := fmt.Sprintf(`sip_listen %s
sip_transports udp
net_interface 127.0.0.1
module_path %s
audio_player aubridge,discard
audio_source ausine,400
audio_alert aubridge,discard
module stdio.so
module g711.so
module ausine.so
module aubridge.so
module_app account.so
module_app menu.so
module_app contact.so
module_app presence.so
`, address, modules)
	contacts := ""
	if contact != "" {
		contacts = "<" + contact + ">;presence=none\n"
	}
	for name, contents := range map[string]string{"config": config, "accounts": "<" + p.uri + ">;regint=0;answermode=auto;audio_codecs=PCMA\n", "contacts": contacts, "current_contact": contact} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	log, err := os.Create(p.log)
	if err != nil {
		t.Fatal(err)
	}
	processCtx, cancel := context.WithCancel(ctx)
	command := exec.CommandContext(processCtx, executable, "-4", "-f", dir, "-c")
	p.input, err = command.StdinPipe()
	if err != nil {
		cancel()
		log.Close()
		t.Fatal(err)
	}
	command.Stdout, command.Stderr = log, log
	if err := command.Start(); err != nil {
		cancel()
		p.input.Close()
		log.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		p.input.Close()
		command.Wait()
		log.Close()
		if t.Failed() {
			data, _ := os.ReadFile(p.log)
			t.Logf("%s peer log:\n%s", user, data)
		}
	})
	p.waitLog(t, ctx, "baresip is ready")
	return p
}

func (p *peer) command(t *testing.T, command string) {
	t.Helper()
	if _, err := fmt.Fprintln(p.input, command); err != nil {
		t.Fatal(err)
	}
}

func (p *peer) waitLog(t *testing.T, ctx context.Context, text string) {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := os.ReadFile(p.log)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), text) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("waiting for peer log %q: %v", text, ctx.Err())
		case <-ticker.C:
		}
	}
}
