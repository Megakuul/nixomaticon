# Media sessions

`Call` joins SDP, the codec workers, RTP/RTCP transport, and Linux audio devices.
The packaged build enables Opus and SpeexDSP. A build without SpeexDSP can change
codecs only when the codec and retained device PCM rates match.

`AnswerOffer` negotiates an incoming offer. `CurrentOffer` creates a new offer
with the current codec, local hold state, SDES keys, ICE credentials, and an
incremented SDP origin version. `ValidateAnswer` checks an answer against that
pending offer without changing audio or transport state. `AcceptAnswer` validates
and commits against the explicit offer sent on the wire, including local hold
state; signaling serializes that commit with subsequent negotiations. `Connect`
applies an answer against the pending offer; `ConnectEarly` receives provisional audio without opening the
microphone. `AnswerEarlyOffer` applies an early-dialog UPDATE only after provisional
playback exists, and keeps capture closed through codec changes. It rejects
confirmed calls. Signaling must pass the latest negotiated remote SDP to `Connect`
when the initial INVITE completes; that is when capture opens.

Codec, payload mapping, and supported codec-parameter changes replace the
capture/playback workers after the new codec and resamplers are prepared. Native
audio devices and the echo canceller retain their original PCM rate. Statistics
report both `SampleRate` (codec PCM) and `DeviceSampleRate`. Changing to a wider
codec cannot recover frequencies excluded by an initially narrowband device
stream. RTP clock-rate changes start a new sender SSRC and reset its RTCP sender
counters; cumulative call counters remain available.

Recording and conference mixing use the retained device PCM rate. Calls can
change between G.711, G.722, and Opus while keeping the same stereo WAV format
and conference links; Speex resamples at the codec boundaries and between
conference devices with different rates. Device recovery reopens the retained
device rate. Rejected codec, security, or ICE changes leave the active
workers and negotiated transport intact. Trickle ICE remains unsupported.

`RestartOffer(ctx, advertisedIP)` prepares a transport-only ICE restart. Call it
inside signaling's serialized offer builder, then pass the exact offer and answer
to `AcceptAnswer`; call `CancelRestart(offer)` if SIP rejects or cancels it.
Repeated offer construction reuses the pending credentials. `AnswerOffer`
recognizes remote restarts and returns newly gathered credentials before
connectivity checks finish. `ICERestartPending`, `ICEState`, and `LastError`
report progress and failures.

A restart keeps the old RTP/SRTP path, codecs, audio devices, recording, and
conference links while a separate Pion ICE agent gathers and checks candidates.
Only a nominated replacement switches the path; failure retains the existing
path. There is one pending replacement per call, with five seconds for gathering,
ten seconds for checks, and thirty seconds for an unanswered local offer.
Call closure cancels and joins this work. Restart SDP uses the replacement port.

Both ICE credentials must change, and combined codec, key, telephone-event, or
direction changes are rejected. The original controlling/controlled role is
retained from authenticated ICE requests; full ICE peers whose role is not yet
confirmed must wait before retrying. A session-level `a=ice-lite` makes Voiper
the controlling full agent, including when it answers the SIP call. Restarts
retain that role and reject changes between full and lite mode. These boundaries follow
[RFC 8445 section 9](https://www.rfc-editor.org/rfc/rfc8445.html#section-9).

`Settings.MediaSecurity="dtls"` requires DTLS-SRTP and verified TLS signaling
for the SDP fingerprints. Each call has a fresh self-signed certificate and
random association identity. Offers use `UDP/TLS/RTP/SAVP`, SHA-256 fingerprints,
`setup:actpass`, and RTCP multiplexing. Incoming `UDP/TLS/RTP/SAVPF` is also
accepted. Pion DTLS 1.2 verifies the peer certificate against the SDP identity
and negotiates AES-128-GCM or AES128-CM/HMAC-SHA1-80 SRTP; Pion SRTP derives the
traffic keys from the DTLS exporter. Extended Master Secret is required.

No microphone is opened and no plaintext RTP/RTCP is sent while the DTLS
handshake is incomplete. Handshakes have a ten-second deadline and call closure
interrupts them. DTLS packets use a bounded queue separate from RTP and RTCP.
`Encrypted` becomes true only after keys are installed; remote DTLS closure
stops media and appears in `LastError`.

Hold, codec changes, and ICE restarts retain the established association and
SRTP replay state. The SDP association identity follows
[RFC 8842](https://www.rfc-editor.org/rfc/rfc8842.html): subsequent offers use
`actpass` and keep the existing `tls-id`. Changing the fingerprint set, setup
role, or association identity is rejected; creating a replacement DTLS
association during a call is not yet implemented. Non-ICE media address changes
are also rejected. Plain RTP and SDES cannot downgrade an established DTLS call.

`SendDTMF` returns `ErrTelephoneEventsNotNegotiated` only for absent RTP event
capability or an unsupported digit. Signaling can use that result to select SIP
INFO; closed or held calls and full queues must not trigger a fallback.

`Close` starts one owned cleanup operation and waits at most two seconds. It
returns `ErrCleanupPending` when a native operation has not finished.
`Stats.CleanupPending` remains true until cleanup completes, and a later `Close`
returns the final retained error. Native initialization and teardown cannot be
forcibly interrupted; resources are retained until their callbacks can safely
finish.

Run the package checks in the cached development shell:

```sh
nix --offline develop path:. --command go test -race ./pkg/audio ./pkg/codec ./pkg/rtp ./pkg/media
```

Tests use synthetic PCM and loopback SIP/RTP/ICE/TURN plus an independently constructed Pion DTLS peer. They cover codec and clock
changes, RTP/SRTP and TURN ICE restarts, failed restart path preservation, bodyless re-INVITE offer/answer validation, cancellation, recording and
conference tone routing across codec changes, and stalled native cleanup. Physical devices, acoustic echo
paths, and production NAT/server combinations require deployment testing.

`Stats.MediaFlow` and `MediaWarning` report absent RTP after a five-second grace
period. Direction negotiation, early receive-only playback, hold, DTLS startup,
closed calls, and unavailable devices have separate states. These are packet-flow
diagnostics, not proof of audible speech at either end. Silence suppression can
explain missing incoming packets; the warning says so. Statistics use snapshots,
never wait on the network, and never terminate a call automatically.

The Opus capture worker applies fresh RTCP receiver reports for its own SSRC.
At most once per five-second reporting interval, it updates libopus's expected
loss and conservatively reduces bitrate by 10% for loss of at least 5%, down to
12 kbit/s (or the lower negotiated cap). Three good intervals at no more than 1%
loss allow a 1 kbit/s increase, up to the initial negotiated voice cap. Reports
older than fifteen seconds, duplicate reports, and out-of-order reports do not
retune the encoder. FEC remains controlled by SDP negotiation. DTX is not enabled;
loss reports are not bandwidth estimates or a complete congestion controller.
The UI exposes encoder bitrate, peer-reported loss, and the age of that report.
See [RFC 3550 reception reports](https://www.rfc-editor.org/rfc/rfc3550.html#section-6.4.1)
and the [libopus encoder controls](https://www.opus-codec.org/docs/opus_api-1.6/group__opus__encoderctls.html).

`Settings.SymmetricRTP` optionally permits same-IP port changes for required
SDES-SRTP over verified TLS signaling. It is disabled by default and never learns
plain RTP peers, changes the signaled IP, or overrides ICE/DTLS paths. Pion must
authenticate and replay-check each packet before a port can change. RTP sequence
ordering and the authenticated SRTCP index prevent delayed packets from restoring
an old port. RTP and RTCP learn their own ports unless multiplexed; a new SSRC
must first be validated on the selected path. This helps NAT port rebinding where
the signaled IP is already correct; ICE/TURN is needed for broader traversal.
