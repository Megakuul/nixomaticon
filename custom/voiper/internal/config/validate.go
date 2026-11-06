package config

import (
	"errors"
	"fmt"
	"github.com/pion/stun/v4"
	"strings"
)

func Validate(c Config) error {
	if c.MaxRedirects < 0 || c.MaxRedirects > 5 {
		return errors.New("redirect limit must be between 0 and 5")
	}
	switch c.ICEPolicy {
	case "", "disabled", "auto", "required":
	default:
		return errors.New("invalid ICE policy")
	}
	if len(c.ICEServers) > 8 {
		return errors.New("up to eight ICE servers are supported")
	}
	urls := 0
	for _, server := range c.ICEServers {
		if len(server.Username) > 1024 || len(server.Credential) > 4096 {
			return errors.New("ICE credentials are too long")
		}
		for _, url := range server.URLs {
			urls++
			if len(url) > 2048 {
				return errors.New("ICE server URL is too long")
			}
			if _, err := stun.ParseURI(url); err != nil {
				return errors.New("invalid STUN or TURN URL")
			}
		}
	}
	if urls > 16 {
		return errors.New("up to sixteen ICE server URLs are supported")
	}

	if strings.TrimSpace(c.Server) == "" || strings.TrimSpace(c.Username) == "" {
		return errors.New("SIP server and username are required")
	}
	if c.Port < 0 || c.Port > 65535 {
		return errors.New("SIP port must be between 0 (automatic) and 65535")
	}
	for _, value := range []string{c.Server, c.Username, c.Domain, c.AuthUsername, c.DisplayName, c.OutboundProxy, c.LocalAddress, c.MediaAddress, c.ForwardAlways, c.ForwardBusy, c.ForwardNoAnswer, c.TLSCAFile, c.SwyxUsername} {
		if len(value) > 2048 || strings.ContainsAny(value, "\r\n\x00") {
			return errors.New("account field contains invalid characters or is too long")
		}
	}
	if len(c.SwyxPassword) > 4096 {
		return errors.New("Swyx password is too long")
	}
	switch strings.ToLower(c.Transport) {
	case "", "udp", "tcp", "tls":
	default:
		return errors.New("choose UDP, TCP or TLS transport")
	}
	switch c.MediaSecurity {
	case "", "disabled", "optional", "required", "dtls":
	default:
		return errors.New("invalid media security policy")
	}
	if (c.MediaSecurity == "required" || c.MediaSecurity == "dtls") && !strings.EqualFold(c.Transport, "tls") {
		return errors.New("required media encryption needs TLS signaling")
	}
	if c.SymmetricRTP && c.MediaSecurity != "required" {
		return errors.New("authenticated RTP port changes require SDES-SRTP with TLS signaling")
	}
	if c.NoAnswerSeconds < 0 || c.NoAnswerSeconds > 300 {
		return errors.New("no-answer delay must be between 5 and 300 seconds, or 0 for the default")
	}
	if c.NoAnswerSeconds > 0 && c.NoAnswerSeconds < 5 {
		return errors.New("no-answer delay must be at least 5 seconds")
	}
	if len(c.Features) > 20 {
		return errors.New("an account supports up to 20 PBX feature codes")
	}
	names := map[string]bool{}
	for _, feature := range c.Features {
		if feature.Name == "" || len(feature.Name) > 80 || feature.Target == "" || len(feature.Target) > 1024 || strings.ContainsAny(feature.Target, "\r\n\x00") {
			return errors.New("PBX features need a name and valid target")
		}
		if names[feature.Name] {
			return fmt.Errorf("duplicate PBX feature name %q", feature.Name)
		}
		names[feature.Name] = true
		if feature.Action != "" && feature.Action != "dial" && feature.Action != "transfer" {
			return errors.New("PBX feature action must be dial or transfer")
		}
	}
	seen := map[string]bool{}
	for _, codec := range c.Codecs {
		codec = strings.ToUpper(codec)
		if codec != "OPUS" && codec != "G722" && codec != "PCMA" && codec != "PCMU" {
			return fmt.Errorf("unsupported codec %q", codec)
		}
		if seen[codec] {
			return errors.New("codec preference contains duplicates")
		}
		seen[codec] = true
	}
	return nil
}
