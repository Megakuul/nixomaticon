# Independent SIP and audio peer

Run from the repository root:

```sh
nix develop path:. --command go test -race -tags opus,speex,integration -run '^TestBaresipIndependentPeer$' -count=1 -timeout=2m -v ./pkg/media
```

The opt-in test launches Baresip with a temporary account and configuration. SIP
and RTP stay on IPv4 loopback; no registration server, audio hardware, desktop,
or existing account is used. Each codec and offer mode gets a separate process
and call with a 25-second deadline. Process cancellation and Go call/client cleanup run on
failure as well as success. Temporary logs and WAV files are removed after the
test; the Baresip log is printed on failure.

For PCMU, PCMA, G.722 and Opus, both initial-offer and delayed-offer calls verify:

- An outgoing Voiper INVITE negotiates the requested codec with Baresip.
  Initial-offer calls carry SDP in INVITE; delayed-offer calls send a bodyless
  INVITE and answer Baresip's final offer in ACK. Transport and SDP preparation
  completes in the answer callback; media starts after the connected event.
- Voiper decodes Baresip's 400 Hz sine wave, and Baresip's decoded WAV contains
  Voiper's 700 Hz sine wave. The assertions measure tone energy in decoded PCM,
  rather than merely counting RTP packets.
- Baresip reports reception of the completed RFC 4733 digit `5` event for
  G.711/G.722, or SIP INFO digit `5` for Opus. Baresip advertises an 8 kHz
  telephone-event clock with Opus, while Voiper currently requires the RTP event
  clock to match the audio clock; the fixture exercises INFO explicitly for
  this case only when `SendDTMF` returns `ErrTelephoneEventsNotNegotiated`.
  Other RTP DTMF failures fail the test rather than triggering INFO fallback.
- Hold stops incoming audio packets, and resume restores decoded audio.
- BYE ends the call at both peers.

The development shell builds Baresip with libopus explicitly enabled; the
upstream Nixpkgs package otherwise omits this module. Outside the shell, install
Baresip with `g711`, `g722`, `opus`, `stdio`, `ausine`, `aubridge`, `sndfile`,
`account`, and `menu` modules. If they are not located under
`<baresip-prefix>/lib/baresip/modules`, set `BARESIP_MODULE_PATH` to their directory.
The fixture fails clearly when a required module is missing.

Baresip 4.11.0 is the initial reference version. The private configuration follows
its [example configuration](https://github.com/baresip/baresip/blob/v4.11.0/docs/examples/config),
[synthetic audio source](https://github.com/baresip/baresip/blob/v4.11.0/modules/ausine/ausine.c)
and [decoded audio recorder](https://github.com/baresip/baresip/blob/v4.11.0/modules/sndfile/sndfile.c).
The module calls received RTP telephone events “in-band DTMF” in its log;
this fixture sends negotiated RFC 4733 packets, not audible DTMF tones.

The signaling, conference and repeated-call checks can be run together with the
codec checks:

```sh
nix develop path:. --command go test -race -tags opus,speex,integration -run '^TestBaresip(IndependentPeer|Conference|RepeatedCallCleanup|MessagesAndPresence|IncomingCall|Transfer)$' -count=1 -timeout=2m -v ./pkg/media ./integration/baresip
```

The additional checks exercise these paths against Baresip 4.11.0:

- Bidirectional SIP MESSAGE preserves sender, text and MIME type. An ordinary
  presence subscription receives online and offline PIDF NOTIFY updates, then
  unsubscribes. This requires the `contact` and `presence` Baresip modules.
- Incoming calls from Baresip cover answer followed by remote BYE, rejection
  with 486 and caller cancellation before answer.
- Blind REFER and attended REFER/Replaces connect two independent Baresip peers.
  Voiper receives final successful transfer NOTIFY before releasing its original
  leg. These two cases use inactive SDP and verify signaling, not transfer audio.
- Two active PCMA calls receive different synthetic peer tones. After joining a
  local conference, each peer's decoded WAV contains Voiper's tone plus the other
  peer's tone, with its own tone excluded. Leaving restores separate audio.
- Thirty new SIP clients and media calls dial, decode peer audio, send BYE and
  close. After a five-call warmup, periodic GC and resource measurements reject
  growth above 8 MiB of Go heap, four goroutines or two descriptors. Each cycle's
  cleanup runs before measurement. After the 32-second UDP transaction grace
  period, a final measurement also limits retained heap growth to 1 MiB.
  This covers client/call lifecycle cleanup;
  no registrar or SIP account authentication is involved.

A separate optional long-call check runs for an explicitly requested duration:

```sh
VOIPER_SOAK_DURATION=1h nix develop path:. --command go test -race -tags opus,speex,integration -run '^TestBaresipLongCall$' -count=1 -timeout=65m -v ./pkg/media
```

Without `VOIPER_SOAK_DURATION` that test skips. Accepted durations are 10 seconds
through four hours; increase the Go timeout for longer runs. It maintains a PCMA
call with synthetic duplex audio, checks decoded incoming tone and packet
progress every five seconds, and measures Go heap, goroutines and descriptors
about once a minute. Relative to warmup, the limits are 32 MiB of Go heap,
32 goroutines and eight descriptors. BYE and the peer's final decoded outgoing
tone are checked before cleanup. PCM analysis uses bounded windows; temporary
Baresip WAV files use roughly 115 MB per hour and are removed at test cleanup.
The duration, counters and resource measurements are included in verbose output.

These checks establish specific interoperability with a separate implementation.
They do not establish Swyx interoperability, hardware audio quality, NAT
traversal, TLS/SRTP, desktop interaction or absence of every possible resource
leak. The ordinary test suite and package build do not launch Baresip; use the
explicit `integration` tag and commands above.

Recorded acceptance on 2026-10-07 used Baresip 4.11.0 from the development
shell. The combined command above passed under the Go race detector, including
all eight codec/offer cases and all additional signaling/conference checks.
Thirty client/call cycles kept three goroutines and nine descriptors; after the
transaction grace period the Go heap was 716,744 bytes, below its 1,159,784-byte
warmup measurement.

The separate one-hour race run passed from `2026-10-07T18:17:38Z` through
`2026-10-07T19:17:38Z`. Every five-second audio-progress check passed, reported RTP
loss stayed zero, and the process retained 13 goroutines and 12 descriptors.
The last periodic heap measurement was 978,464 bytes (warmup: 887,568 bytes).
The final peer recording contained Voiper's outgoing tone, and BYE completed.
This is evidence for that loopback PCMA run, not a hardware or Swyx-server test.
