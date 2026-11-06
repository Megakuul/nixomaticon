// Package swyx keeps vendor protocol extensions outside the reusable SIP core.
package swyx

import (
	"slices"
	"strings"
)

type Capabilities struct {
	Server       string
	SwyxDetected bool
	// Presence is how contacts' status is received: standard, dialog, swyx or disabled.
	Presence string
	// Messaging is sip, unavailable or disabled.
	Messaging string
	// Status is how the own status is published: sip, swyx (client REST API) or unavailable.
	Status    string
	Directory string
	Detail    string
}

// Detect chooses providers from the registrar's identification and the methods
// its OPTIONS response allows. allow is nil while that probe has not answered;
// standard SIP features are then assumed.
//
// SwyxWare 14.21 answers MESSAGE and PUBLISH with 405 and lists neither in
// Allow, but serves the client REST API used for own status (see PROTOCOL.md).
func Detect(server string, allow []string, presenceMode, messagingMode string) Capabilities {
	c := Capabilities{Server: server, Presence: "standard", Messaging: "sip", Status: "sip", Directory: "local"}
	name := strings.ToLower(server)
	c.SwyxDetected = strings.Contains(name, "swyx") || strings.Contains(name, "netphone")
	allows := func(method string) bool {
		return allow == nil || slices.ContainsFunc(allow, func(m string) bool { return strings.EqualFold(m, method) })
	}
	switch presenceMode {
	case "disabled", "swyx", "dialog", "standard":
		c.Presence = presenceMode
	default:
		if c.SwyxDetected {
			c.Presence = "swyx"
		}
	}
	switch {
	case c.Presence == "disabled":
		c.Status = "unavailable"
	case c.SwyxDetected && c.Presence == "swyx":
		c.Status = "swyx"
	case !allows("PUBLISH"):
		c.Status = "unavailable"
	}
	switch messagingMode {
	case "disabled":
		c.Messaging = "disabled"
	case "standard", "sip":
		// An explicit choice overrides the server's Allow list.
	default:
		if !allows("MESSAGE") {
			c.Messaging = "unavailable"
		}
	}

	detail := []string{"Standard SIP."}
	if c.SwyxDetected {
		detail = []string{"SwyxWare detected."}
	}
	switch c.Status {
	case "swyx":
		detail = append(detail, "Your status, do not disturb and status text are set through the Swyx client API with your SIP credentials.")
	case "unavailable":
		detail = append(detail, "The server does not accept status publication (PUBLISH).")
	}
	switch c.Messaging {
	case "unavailable":
		if c.SwyxDetected {
			detail = append(detail, "Chat: the server rejects SIP MESSAGE; Swyx chat runs on Swyx Messenger, which Voiper cannot reach.")
		} else {
			detail = append(detail, "Chat: the server does not accept SIP MESSAGE.")
		}
	case "disabled":
		detail = append(detail, "Chat is disabled.")
	}
	c.Detail = strings.Join(detail, " ")
	return c
}
