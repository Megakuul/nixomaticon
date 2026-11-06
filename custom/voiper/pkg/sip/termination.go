package sip

import (
	"mime"
	"strings"

	wire "github.com/emiago/sipgo/sip"
)

// Interpret only the standardized cause. A quoted text parameter can itself
// contain commas or SIP-looking strings and must never supply a second reason.
func completedElsewhere(req *wire.Request) bool {
	for _, header := range req.GetHeaders("Reason") {
		value := header.Value()
		if len(value) > 2048 {
			continue
		}
		start := 0
		quoted, escaped := false, false
		for i := 0; i <= len(value); i++ {
			if i < len(value) {
				character := value[i]
				if escaped {
					escaped = false
					continue
				}
				if quoted && character == '\\' {
					escaped = true
					continue
				}
				if character == '"' {
					quoted = !quoted
				}
				if character != ',' || quoted {
					continue
				}
			}
			protocol, parameters, err := mime.ParseMediaType(strings.TrimSpace(value[start:i]))
			if err == nil && protocol == "sip" && parameters["cause"] == "200" {
				return true
			}
			start = i + 1
		}
	}
	return false
}

func sameCanceledInvite(invite, cancel *wire.Request) bool {
	if cancel.CallID() == nil || cancel.CSeq() == nil || cancel.From() == nil || cancel.To() == nil || cancel.Via() == nil || invite.Via() == nil {
		return false
	}
	local, _ := invite.To().Params.Get("tag")
	cancelLocal, _ := cancel.To().Params.Get("tag")
	remote, _ := invite.From().Params.Get("tag")
	cancelRemote, _ := cancel.From().Params.Get("tag")
	branch, _ := invite.Via().Params.Get("branch")
	cancelBranch, _ := cancel.Via().Params.Get("branch")
	return cancel.Method == wire.CANCEL && cancel.Source() == invite.Source() &&
		*cancel.CallID() == *invite.CallID() && cancel.CSeq().SeqNo == invite.CSeq().SeqNo &&
		cancel.Recipient.String() == invite.Recipient.String() && branch != "" && branch == cancelBranch &&
		local == cancelLocal && remote == cancelRemote
}
