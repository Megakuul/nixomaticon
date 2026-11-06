package swyx

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

const presenceNamespace = "http://sip.lanphone.de/presence/"

var ErrUnsupportedPresence = errors.New("Swyx Classic presence extension is absent")

type Presence struct {
	Entity     string
	State      string
	Note       string
	Source     string
	UserStatus string
}

// ParsePresence interprets the Classic PIDF extension documented in PROTOCOL.md.
// The caller owns subscription identity, expiry, and event timestamps.
func ParsePresence(body []byte) (Presence, error) {
	if len(body) > 64*1024 {
		return Presence{}, errors.New("presence body exceeds 64 KiB")
	}
	var document struct {
		XMLName xml.Name `xml:"urn:ietf:params:xml:ns:pidf presence"`
		Entity  string   `xml:"entity,attr"`
		Note    string   `xml:"urn:ietf:params:xml:ns:pidf note"`
		Tuples  []struct {
			Note   string `xml:"urn:ietf:params:xml:ns:pidf note"`
			Status struct {
				UserStatuses []string `xml:"http://sip.lanphone.de/presence/ userstatus"`
			} `xml:"urn:ietf:params:xml:ns:pidf status"`
		} `xml:"urn:ietf:params:xml:ns:pidf tuple"`
	}
	decoder := xml.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&document); err != nil {
		return Presence{}, fmt.Errorf("parse presence: %w", err)
	}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Presence{}, fmt.Errorf("parse presence: %w", err)
		}
		if text, ok := token.(xml.CharData); !ok || strings.TrimSpace(string(text)) != "" {
			return Presence{}, errors.New("unexpected content after presence document")
		}
	}
	result := Presence{Entity: document.Entity, State: "unknown", Source: "swyx-classic", Note: strings.TrimSpace(document.Note)}
	found := false
	conflict := false
	for _, tuple := range document.Tuples {
		for _, status := range tuple.Status.UserStatuses {
			status = strings.ToLower(strings.TrimSpace(status))
			if found && status != result.UserStatus {
				conflict = true
			}
			if !found {
				result.UserStatus = status
				if result.Note == "" {
					result.Note = strings.TrimSpace(tuple.Note)
				}
			}
			found = true
		}
	}
	if !found {
		return Presence{}, ErrUnsupportedPresence
	}
	if strings.TrimSpace(result.Entity) == "" {
		return Presence{}, errors.New("presence entity is missing")
	}
	if !conflict {
		switch result.UserStatus {
		case "logged on":
			result.State = "available"
		case "logged off":
			result.State = "offline"
		case "active":
			result.State = "in-call"
		case "away":
			result.State = "away"
		case "donotdisturb":
			result.State = "dnd"
		}
	}
	return result, nil
}
