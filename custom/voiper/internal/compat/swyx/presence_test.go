package swyx

import (
	"errors"
	"strings"
	"testing"
)

func presenceBody(status string) []byte {
	return []byte(`<presence xmlns="urn:ietf:params:xml:ns:pidf" xmlns:v="http://sip.lanphone.de/presence/" entity="sip:alice@example.test"><tuple id="desk"><status><basic>open</basic><v:userstatus>` + status + `</v:userstatus></status><note>At the desk</note></tuple></presence>`)
}

func TestClassicPresenceStates(t *testing.T) {
	for wire, state := range map[string]string{
		"logged on": "available", "logged off": "offline", "active": "in-call",
		"away": "away", "donotdisturb": "dnd", "new vendor state": "unknown",
		" LOGGED ON ": "available",
	} {
		t.Run(wire, func(t *testing.T) {
			p, err := ParsePresence(presenceBody(wire))
			if err != nil {
				t.Fatal(err)
			}
			if p.State != state || p.Entity != "sip:alice@example.test" || p.Source != "swyx-classic" || p.Note != "At the desk" {
				t.Fatalf("unexpected presence: %+v", p)
			}
		})
	}
}

func TestPresenceNamespaceIsolation(t *testing.T) {
	body := strings.ReplaceAll(string(presenceBody("active")), presenceNamespace, "https://unrelated.example/presence")
	if _, err := ParsePresence([]byte(body)); !errors.Is(err, ErrUnsupportedPresence) {
		t.Fatalf("unrelated extension should permit standard fallback: %v", err)
	}
}

func TestMalformedPresence(t *testing.T) {
	for _, body := range [][]byte{
		[]byte(`<presence>`),
		[]byte(strings.Repeat("x", 64*1024+1)),
		append(presenceBody("active"), []byte(`<another/>`)...),
		[]byte(strings.ReplaceAll(string(presenceBody("active")), ` entity="sip:alice@example.test"`, "")),
	} {
		if _, err := ParsePresence(body); err == nil {
			t.Fatal("accepted invalid presence")
		}
	}
}

func TestConflictingPresence(t *testing.T) {
	body := strings.Replace(string(presenceBody("active")), "</status>", "<v:userstatus>logged on</v:userstatus></status>", 1)
	p, err := ParsePresence([]byte(body))
	if err != nil || p.State != "unknown" {
		t.Fatalf("conflicting statuses must stay unknown: %+v, %v", p, err)
	}
}

func TestCapabilitiesKeepMessagingIndependent(t *testing.T) {
	c := Detect("SwyxWare", nil, "auto", "auto")
	if !c.SwyxDetected || c.Presence != "swyx" || c.Status != "swyx" || c.Messaging != "sip" {
		t.Fatalf("unexpected capabilities: %+v", c)
	}
	c = Detect("SwyxWare", nil, "standard", "disabled")
	if c.Presence != "standard" || c.Status != "sip" || c.Messaging != "disabled" {
		t.Fatalf("manual provider selection ignored: %+v", c)
	}
}

// The Allow list SwyxWare 14.21 returns for OPTIONS.
var swyxAllow = []string{"REGISTER", "INVITE", "ACK", "CANCEL", "BYE", "NOTIFY", "REFER", "OPTIONS", "INFO", "SUBSCRIBE", "UPDATE"}

func TestCapabilitiesFollowAllowedMethods(t *testing.T) {
	c := Detect("Swyx IpPbxSrv/14.21", swyxAllow, "auto", "auto")
	if c.Status != "swyx" || c.Messaging != "unavailable" {
		t.Fatalf("Swyx without MESSAGE: %+v", c)
	}
	c = Detect("Asterisk", swyxAllow, "auto", "auto")
	if c.Status != "unavailable" || c.Messaging != "unavailable" {
		t.Fatalf("server without PUBLISH and MESSAGE: %+v", c)
	}
	c = Detect("Asterisk", swyxAllow, "auto", "standard")
	if c.Messaging != "sip" {
		t.Fatalf("explicit SIP messaging must override Allow: %+v", c)
	}
}
