// Package diagnostics exports an explicit allowlist, never application snapshots.
package diagnostics

import (
	"encoding/json"
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode"
)

type Input struct {
	Version            string
	AccountStates      []string
	Calls              []Call
	Audio              Audio
	DeviceNames        []string
	IncludeDeviceNames bool
}
type Audio struct {
	Backend                            string
	EchoCancellation, NoiseSuppression bool
}
type Call struct {
	EncoderBitrate                                                                         int
	ReceiverReportAvailable                                                                bool
	RemotePacketLossPercent, ReceiverReportAgeSeconds                                      float64
	SendIdleSeconds, ReceiveIdleSeconds                                                    float64
	ICERestartPending                                                                      bool
	State, Direction, Codec, Transport, ICEState, ICECandidateType                         string
	Encrypted, Muted, Held, EarlyMedia, AudioStopped, AudioRecovering, Conference, RTCPMux bool
	SampleRate, ClockRate, PacketizationMilliseconds, BufferMilliseconds                   int
	PacketsSent, PacketsReceived, PacketsLost, PacketsDropped, BytesSent, BytesReceived    uint64
	CaptureOverruns, PlaybackUnderruns, AudioRecoveryAttempts                              uint64
	InputLevel, OutputLevel, JitterMilliseconds, RTTMilliseconds                           float64
}

var versionPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:[-+][a-zA-Z0-9.-]+)?$`)

func Export(input Input) ([]byte, error) {
	if len(input.AccountStates) > 256 || len(input.Calls) > 256 || len(input.DeviceNames) > 64 {
		return nil, fmt.Errorf("diagnostic snapshot exceeds limits")
	}
	version := "unknown"
	if len(input.Version) <= 64 && versionPattern.MatchString(input.Version) {
		version = input.Version
	}
	states := map[string]int{}
	for _, state := range input.AccountStates {
		states[known(state, "registering", "registered", "failed", "disabled", "unregistered", "retrying")]++
	}
	calls := make([]map[string]any, 0, len(input.Calls))
	for i, c := range input.Calls {
		// Copy fields deliberately: new application fields must not enter exports implicitly.
		calls = append(calls, map[string]any{
			"index":             i + 1,
			"state":             known(c.State, "calling", "ringing", "ringback", "answering", "connecting", "connected", "redirecting", "declined", "ended"),
			"direction":         known(c.Direction, "incoming", "outgoing"),
			"codec":             known(strings.ToUpper(c.Codec), "OPUS", "G722", "PCMA", "PCMU"),
			"transport":         known(strings.ToUpper(c.Transport), "RTP", "SRTP", "RTP/AVP", "RTP/SAVP", "UDP/TLS/RTP/SAVP", "UDP/TLS/RTP/SAVPF"),
			"iceState":          known(c.ICEState, "new", "gathering", "gathered", "checking", "restarting", "connected", "completed", "disconnected", "failed", "closed", "disabled", "fallback"),
			"iceCandidateType":  known(c.ICECandidateType, "host", "srflx", "prflx", "relay"),
			"iceRestartPending": c.ICERestartPending,
			"encrypted":         c.Encrypted, "muted": c.Muted, "held": c.Held, "earlyMedia": c.EarlyMedia,
			"audioStopped": c.AudioStopped, "audioRecovering": c.AudioRecovering, "conference": c.Conference, "rtcpMux": c.RTCPMux,
			"sampleRate": c.SampleRate, "clockRate": c.ClockRate, "packetizationMilliseconds": c.PacketizationMilliseconds, "bufferMilliseconds": c.BufferMilliseconds,
			"packetsSent": c.PacketsSent, "packetsReceived": c.PacketsReceived, "packetsLost": c.PacketsLost, "packetsDropped": c.PacketsDropped, "bytesSent": c.BytesSent, "bytesReceived": c.BytesReceived,
			"captureOverruns": c.CaptureOverruns, "playbackUnderruns": c.PlaybackUnderruns, "audioRecoveryAttempts": c.AudioRecoveryAttempts,
			"inputLevel": c.InputLevel, "outputLevel": c.OutputLevel, "jitterMilliseconds": c.JitterMilliseconds, "rttMilliseconds": c.RTTMilliseconds,
			"encoderBitrate": c.EncoderBitrate, "receiverReportAvailable": c.ReceiverReportAvailable,
			"remotePacketLossPercent": c.RemotePacketLossPercent, "receiverReportAgeSeconds": c.ReceiverReportAgeSeconds,
			"sendIdleSeconds": c.SendIdleSeconds, "receiveIdleSeconds": c.ReceiveIdleSeconds,
		})
	}
	report := map[string]any{
		"schemaVersion": 1, "generatedAt": time.Now().UTC().Format(time.RFC3339),
		"applicationVersion": version,
		"system":             map[string]string{"os": runtime.GOOS, "architecture": runtime.GOARCH, "goVersion": runtime.Version()},
		"accountStates":      states, "calls": calls,
		"audio": map[string]any{"backend": known(strings.ToLower(input.Audio.Backend), "auto", "pulse", "pulseaudio", "pipewire", "alsa", "null"), "echoCancellation": input.Audio.EchoCancellation, "noiseSuppression": input.Audio.NoiseSuppression},
	}
	if input.IncludeDeviceNames {
		names := make([]string, 0, len(input.DeviceNames))
		for _, name := range input.DeviceNames {
			clean := []rune(strings.Map(func(r rune) rune {
				if unicode.IsControl(r) {
					return -1
				}
				return r
			}, name))
			if len(clean) > 128 {
				clean = clean[:128]
			}
			names = append(names, string(clean))
		}
		report["deviceNames"] = names
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("invalid diagnostic numeric statistics: %w", err)
	}
	return append(data, '\n'), nil
}

func known(value string, allowed ...string) string {
	for _, candidate := range allowed {
		if value == candidate {
			return value
		}
	}
	return "unknown"
}
