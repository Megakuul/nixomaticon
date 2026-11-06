# Voiper

A Linux desktop SIP softphone built with Go, Wails and Svelte. NixOS on
`x86_64-linux` is the primary build target. It provides ordinary SIP calling,
wideband audio, multiple accounts, transfers, conferences, contacts and messaging.
SwyxIt! Classic status support is isolated from the standard SIP implementation.
Independent Baresip interoperability and private native audio services are tested;
physical headset quality and a live SwyxWare installation remain separate acceptance gates.

## Build and run

```sh
nix develop
pnpm --dir web install --frozen-lockfile
make dev-voiper
```

For the packaged application, including its native audio and desktop libraries:

```sh
nix build .#voiper
./result/bin/voiper
# or
nix run .#voiper
```

When using newly created files that have not yet been added to Git, use
`nix develop path:.`, `nix build path:.#voiper` and `nix flake check path:.`.
The flake pins Go 1.27.1, Node 24.21.0, pnpm 12.9.0 and Wails 2.16.0.
The frontend uses Svelte 5 and Vite 8. The package builds frontend assets offline
from the lockfile, then builds Go with GTK3, WebKitGTK 4.1, libopus, SpeexDSP and libsecret.

## Desktop rendering troubleshooting

The browser opened during development and the Linux desktop window use different
renderers: the desktop uses WebKitGTK. If hover effects or menus flicker only in
the desktop window, fully quit the app and compare the same binary with:

```sh
WEBKIT_DISABLE_DMABUF_RENDERER=1 ./result/bin/voiper
```

This isolates WebKit's DMA-BUF rendering path without changing the UI. If it still
flickers, quit again and try disabling compositing for comparison:

```sh
WEBKIT_DISABLE_COMPOSITING_MODE=1 ./result/bin/voiper
```

These are diagnostic overrides, not application defaults. Disabling compositing
can increase CPU usage and slow scrolling. Record which command helps, the GPU
and driver, and whether the session uses Wayland or X11 before choosing a lasting
workaround. For development, prefix `make dev-voiper` with the same variable.
Software-rendered Xvfb checks do not validate the desktop GPU rendering path.

## Frontend styling

The UI uses Tailwind 4 utilities in Svelte components. Shared control class lists
live in `web/src/ui.js`; theme colors and shadows are defined in `web/src/app.css`.
Keep new layout, spacing, responsive behavior and interaction states in utilities.
CSS is reserved for the global theme, browser-specific control drawing and
animation keyframes. Keep complete utility names in source so Tailwind can find them.
Run `pnpm --dir web build` inside `nix develop` after frontend changes.

## First call

1. In **Accounts**, create an account with your server, username and password.
   Expand advanced settings for an authentication username, SIP domain,
   outbound proxy, transport or advertised media address.
2. Enable the account and wait for **registered**. Select it as the outgoing
   account. Encrypted account files require their key each time they are enabled.
3. In **Audio & settings**, choose the microphone, output and optional ringer.
   **Test microphone** measures your voice for five seconds; **Play test sound**
   checks the selected output. Save the sound settings when ready. Automatic
   selection tries PulseAudio first; PipeWire works through its PulseAudio service.
4. Type an extension or SIP address and press **Enter** or **Call**, or search
   for a contact and select its number. The active-call card has round controls
   for mute, hold, **Keypad** (phone menus), **Transfer**, **Add person** and
   **More** (gain, recording, network audio), plus optional statistics.

PulseAudio/PipeWire shares output with other applications. An explicit ALSA
hardware device may be exclusive. Audio settings include optional SpeexDSP echo
cancellation and noise suppression. Bounded automatic recovery retries the same
selected devices; a failed recovery exposes a manual retry on the call card.
Device selection is changed between calls. Real headset/hotplug quality still
needs acceptance testing.

A new outgoing call or answering another call first holds existing connected
calls. **Add person** searches contacts and calls the new participant while the
current call waits; once two or three calls are connected, **Merge into
conference** resumes held legs and mixes them locally, with SpeexDSP resampling
between codec rates. **Split conference** returns them to separate calls.

**Transfer** opens a contact search. **Transfer now** hands the call over
directly (blind). **Call first** holds the call and rings the recipient; the new
call card then offers **Complete transfer** (attended transfer with Replaces) or
**Cancel and return**, which hangs up the consultation and resumes the caller.
Transfer requires peer support for REFER; acceptance of REFER alone does not end
the original call. Final successful transfer notification releases the original
leg and, for attended transfer, its consultation leg. Failure leaves the calls
available. Incoming transfers place a replacement call and end the original only
after successful completion. Recordings are explicitly started, stored as private
stereo WAV files (local and remote channels), and finalized on stop/hangup.
Existing files are never overwritten.

Opening a `sip:`, `sips:` or simple `tel:` URI prefills the dialer in the running
instance. The user still selects an account and presses Call.

Optional global shortcuts live under **Audio/settings → Desktop & privacy**.
Enable them explicitly for the current session through the desktop permission
dialog. They show the dialer or toggle mute when exactly one unheld call is
connected; ambiguous calls open the dialer instead. Unsupported portals report
unavailability. Local Ctrl+1…6 navigation and Ctrl+K dialer focus work independently.

## Capabilities and boundaries

| Area | Implemented | Remaining work / limits |
| --- | --- | --- |
| SIP | UDP/TCP/TLS, digest registration/refresh, bounded registrar DNS failover, incoming/outgoing calls, forked reliable delayed offers, receive-only early media, PRACK, early/established UPDATE, session timers, hold, INFO DTMF, blind/attended transfer | Fixed configured transport; no NAPTR selection or complete RFC 5626 outbound negotiation. See [SIP support](pkg/sip/README.md) for early-dialog and routing boundaries |
| Media | RTP/RTCP, bounded jitter buffering, loss/jitter/RTT statistics, RFC 4733 DTMF, SDES and DTLS SRTP/SRTCP, ICE/STUN/TURN and transport-only ICE restart | No trickle ICE; no mid-call DTLS identity/association replacement; arbitrary NAT paths remain deployment-dependent |
| Codecs | Opus, G.722, PCMA and PCMU; mid-call changes retain device PCM, recording and conference state | Sample-rate conversion requires SpeexDSP. The Nix package includes Opus and SpeexDSP |
| Audio | PulseAudio/PipeWire-Pulse, ALSA fallback, device tests/levels/gain, SpeexDSP echo/noise processing, recovery, conferences and recording | Native shared mixing and daemon-loss shutdown tested with virtual devices; hardware latency, echo and hotplug quality require physical devices |
| Presence | PIDF publication with refresh/withdrawal, dialog/busy-lamp state, voicemail indication, Swyx Classic status reception, Swyx own status/DND/status text and colleague status text via the SwyxWare client API, away while the screen is locked | Permissions depend on server; the Swyx API login uses the SIP credentials |
| Messaging | SIP text MESSAGE when the server allows it, searchable/paged chats, local unread counts, explicit retry and retention | No Swyx Messenger provider (SwyxWare rejects SIP MESSAGE), attachments or remote read receipts |
| Contacts | Multiple labeled numbers, favorites, CSV field mapping, CSV/vCard preview/import/export, LDAP discovery and cached synchronization | DNS/root-DSE discovery needs a company domain or configured endpoint; proprietary Swyx directory APIs are unverified |
| Desktop | Multiple accounts/calls, asynchronous call setup, history, DND, URI activation, notification actions, tray, autostart, wallet, suspend/network recovery and optional portal shortcuts | Desktop capabilities and portal consent depend on the session; full physical-desktop acceptance remains separate |

Plain RTP is the default interoperability mode. For encrypted audio, select
**required** SDES-SRTP or **DTLS-SRTP**, both with verified TLS signaling. Optional
SDES security permits an unencrypted peer. DTLS validates the SDP SHA-256
fingerprint, negotiates the setup role, exports SRTP keys and keeps media closed
until the handshake succeeds. It requires RTCP multiplexing; a changed DTLS
identity or association requires a new call. Statistics report encryption only
after protection is established. TLS verifies the
server against system certificate trust; an account can add an administrator-provided
CA file. There is no insecure-certificate switch.

For PBX pickup, park, conference rooms or persistent forwarding, save the
administrator-provided feature codes in account settings. Dial and transfer actions
support a `{number}` placeholder. Local always/busy/no-answer forwarding sends a
SIP redirect and requires PBX support; local DND and forwarding require Voiper
to be running. Automatic following of remote redirects is off by default.

ICE is configured per account, with explicit STUN/TURN servers and optional
credentials. Auto mode falls back to ordinary RTP only when the peer omits ICE.
Required mode needs ICE plus RTCP multiplexing. Agreed ICE connectivity failure
is reported; it does not silently bypass the selected route. TURN credentials
are stored in the account profile, so use profile encryption when needed.

For SDES-SRTP accounts using TLS, **Allow authenticated SRTP port changes** is
an optional alternative for a peer whose NAT mapping changes ports. Voiper accepts
a new port only after authenticating a fresh packet from the signaled IP address.
RTP and RTCP are validated separately unless multiplexed. It never learns a plain
RTP peer or changes the peer IP; negotiated ICE and DTLS paths keep their own rules.

On an established ICE call, **Reconnect network audio** negotiates a replacement
path while the current path keeps carrying audio. A failed or canceled attempt
keeps the old local transport and reports the error; it cannot restore a network
path that is already unavailable or guarantee that the peer retains its old path. Restart currently requires unchanged codecs,
encryption, hold direction and telephone events. Full ICE and ICE-lite peers are
supported; switching between those modes during a call and trickle ICE are not.

The advanced account option **Let the server offer audio** sends an outgoing
INVITE without SDP. It answers reliable provisional offers in PRACK and final
offers in ACK, maintaining separate bounded candidates for forked early dialogs.
Only the selected final dialog opens microphone capture. Early UPDATE can replace
a prepared candidate; initial-offer early UPDATE keeps playback receive-only.
Unsupported early exchanges or ICE/DTLS association changes fail explicitly.
Call setup returns promptly to the UI, so navigation and hangup remain usable
while gathering media or waiting for the peer.
Automatic DTMF uses SIP INFO when RTP telephone events were not negotiated,
including peers that offer an incompatible event clock with Opus. Explicit RTP
or INFO account settings remain available.

Ctrl+1 through Ctrl+6 switch the main views; Ctrl+K focuses the dialer. Enter
submits the focused form, and Ctrl+Enter sends a composed message. Closing to
the tray is optional and only enabled when a live tray host is available.
Quitting with active calls requires confirmation in the app.

## SwyxIt! Classic

The isolated [Swyx adapter](internal/compat/swyx/PROTOCOL.md) implements the status
extension documented by the `swyx-support` branch of
[simcrack/twinkle](https://github.com/simcrack/twinkle/tree/swyx-support).
It receives `userstatus` in the `http://sip.lanphone.de/presence/` namespace via
ordinary SIP presence subscriptions and maps available, offline, in-call, away
and DND. Auto mode understands these payloads without adding vendor code to
`pkg/sip`. Server banners are capability hints, not proof of interoperability.

Contacts show the status of internal colleagues automatically: while you browse
or search the phonebook, Voiper subscribes to the extensions of the displayed
contacts through the selected outgoing account (at most 100 at a time; the least
recently shown are released first). The colored status and the colleague's status
text appear on each contact card. Short numeric addresses count as extensions;
mobile and external numbers are marked separately.
Configure the provider under **Accounts → Edit → Provider compatibility and
voicemail → Presence** (Automatic or Swyx for this adapter).

To set your own status, open **Your status & voicemail** in **Overview** or
**Phone**, enter a status text and select **Set available** or **Set away**.
On SwyxWare this writes the Swyx status (see below); elsewhere it is published as
standard SIP presence. **Do not disturb** rejects incoming calls locally and,
on SwyxWare, also sets the Swyx do-not-disturb status. While the screen is
locked you show as away; see **Audio & settings → Preferences** to turn this off
or to set your own lock check command (exit status 0 = locked). Without a
command, Voiper detects hyprlock, swaylock, gtklock, waylock, i3lock and other
locker processes, the GNOME and KDE/freedesktop screensaver interfaces and
logind's `LockedHint`, which covers GNOME, KDE, niri, sway and Hyprland setups.

The left navigation collapses to icons in smaller windows. **Overview** combines
the dialer and active calls with a short recent-people list. Selecting a recent
person fills the number and outgoing account; press **Call** to dial. The full
phonebook, search and colleague status remain in **Contacts**.

Connection details, logs and diagnostic export are available from the three
icons at the bottom right. Media errors that do not stop a call, such as
isolated RTP packet loss or decoder concealment, appear only in the live
diagnostics panel; the call card shows media problems only when no audio flows. The live panel closes with its close button or Escape.

Choose Standard, Dialog/busy-lamp, Swyx or Disabled per account. Status publication
uses standard PIDF PUBLISH, refreshes using the server-granted lifetime, and recovers
expired tags and transient failures. **Stop publishing** withdraws it; disabling an
account attempts withdrawal before closing signaling. Authentication/permission
failures stop automatic retries and remain visible. Contact/voicemail watches
recover transient subscription failures with bounded backoff and server retry
delays, replacing expired dialogs while rejecting their stale notifications.
Terminal permission/resource rejections stay unknown until explicitly watched again. Resume/network recovery also
refreshes active publications. The fork does not establish how to publish Swyx
custom states. Publication follows [RFC 3903](https://www.rfc-editor.org/rfc/rfc3903).

### SwyxWare status through the client API

After registering, Voiper asks the registrar for its methods (SIP OPTIONS).
SwyxWare 14.21 lists neither PUBLISH nor MESSAGE and answers both with 405, so
for Swyx accounts own status, do not disturb and status text go through the
SwyxWare client REST API on port 9100, the same API SwyxIt! uses
([evidence](internal/compat/swyx/PROTOCOL.md#live-server-probe-2026-10-15)).
It logs in with the account's SIP user name and password; if Swyx uses a
different password for the client login, the account shows the rejection under
diagnostics. The server's self-signed certificate is trusted on first use and
pinned; a changed certificate is refused until you confirm **Trust the new
server certificate**. An account CA file, when configured, is used instead.
Voiper only writes the fields you change, so a status set in SwyxIt! survives
starting Voiper. The same API supplies the colleagues' status text, which
completes the SIP status on contact cards and in chats.

Chat follows the same probe: servers that allow MESSAGE use SIP chat. SwyxWare
does not, and its chat runs on Swyx Messenger (Classic IM or the Coligo
service), whose wire protocol is not part of the client API and is not
implemented; the chat view says so instead of sending messages that fail.

## Contacts and company directory

Contacts are stored in `contacts.toml` (see [Data and diagnostics](#data-and-diagnostics)).
Swyx phonebook exports (XML), CSV and vCard are import formats only: imports
preview additions, duplicates and invalid records before an atomic commit. A Swyx
export keeps display names and companies; the number with published presence
becomes the primary address and mobile numbers are added with a Mobile label. Foreign CSV exports can map first/last name, primary number, notes
and up to twenty additional number columns with labels. Mapping uses explicit
comma-separated headers, groups one source row into one contact, and keeps empty
number rows visible as errors. Both input and mapped output are limited to 4 MiB;
imports support 10,000 contacts. Exact primary addresses identify existing
contacts. Optional merging appends missing numbers while preserving existing
names, notes, labels, favorites and account preferences.

Under **Contacts → Company directory**, enter an administrator-provided LDAP URL,
base DN and optional bind identity. **Find servers** queries LDAP DNS service
records for an entered company domain; **Find directory bases** reads root-DSE
naming contexts. Neither assumes that a SIP server also hosts a directory.
LDAP requires verified StartTLS; LDAPS verifies certificates. An optional CA file
adds administrator-provided trust. Preview/import is available independently of
automatic synchronization.

Enable synchronization for bounded, paged background refreshes and an offline
cache. Local edits are kept separately; remote refresh does not overwrite them.
Deleting a synchronized contact hides it from later scans, including disappearance
and reappearance. Only a complete successful scan removes unedited remote entries
that disappeared. Partial scans, errors and duplicate conflicts preserve local
contacts. **Restore directory name and numbers** restores the latest cached remote name and
numbers while retaining personal notes, favorite and account settings. Changing
directory identity preserves the previous phonebook as an imported snapshot.

Directory passwords stay in memory for the session or, when selected, the desktop
wallet; they are never written to the contact database. Saved credentials are
reused only for the configured endpoint and bind identity. Without directory
access, imports, favorites and saving recent callers/message correspondents work
locally. No live Swyx directory has been used to validate this integration.

## Data and diagnostics

Accounts live in `$XDG_CONFIG_HOME/voiper/accounts` (normally
`~/.config/voiper/accounts`); audio settings use `voiper/audio.json` and desktop preferences use
`voiper/preferences.json`. Optional desktop-wallet storage keeps the SIP password
out of the account file; whole-profile encryption is a separate option.
Contacts live in `$XDG_CONFIG_HOME/voiper/contacts.toml` as `[[contacts]]`
entries with `name`, `company`, `address`, optional `[[contacts.numbers]]` and
personal fields. Voiper rewrites the file atomically on changes, so edit it while
Voiper is closed; an unreadable file is reported instead of being replaced.
Contacts from earlier versions move there from the database on first start.
History, messages and the directory-sync cache live in
`$XDG_DATA_HOME/voiper/voiper.db` (normally `~/.local/share/voiper/voiper.db`).
Use `voiper --base /path/to/accounts` for existing TOML/encrypted account files.
Back up that account directory, `contacts.toml` and the database with the app closed.
Changing encryption modes requires a different account name, preventing accidental
plaintext copies of an encrypted account under the same name.

The package excludes repository `configs/` from its build inputs, but a flake
source snapshot can still enter the Nix store. Keep real credentials outside the
checkout. Account encryption covers account files, not contacts, the message database
or user-selected WAV recordings.

The package installs third-party licenses under `share/licenses/voiper`:
`go/` holds the license of every Go module linked into the binary, collected
with [go-licenses](https://github.com/google/go-licenses) (`go/licenses.csv`
summarizes them), plus the Go standard library license; `frontend/` holds the
licenses reported by `pnpm licenses` for all frontend packages, including build
tools (`frontend/licenses.json`). Native libraries such as GTK, WebKitGTK, Opus
and SpeexDSP are linked dynamically from their own Nix packages.

Call statistics are opt-in in the Phone view. Missing-packet warnings distinguish
hold, negotiated one-way media and device failure, and allow a startup/resume grace
period. Remote silence suppression can also explain missing inbound packets; a
warning never ends the call automatically. Opus uses fresh peer RTCP loss reports
to reduce bitrate conservatively and recover slowly within the negotiated cap;
statistics show current encoder bitrate, peer loss and report age. This is loss
adaptation, not an estimate of available bandwidth.

Call statistics are opt-in in the Phone view. Diagnostics keeps the last 500
application log entries. The Diagnostics view exports an anonymous allowlisted call
diagnostics report. History retains 2,000 calls; history and message views are
searchable and paginated. Message retention is configurable and defaults to
keeping messages; deletion is explicit. If a call has no audio,
check the selected devices, advertised media address, peer firewall and RTP path.
Configure ICE and STUN/TURN where the peer supports them. Try per-account INFO DTMF for
Swyx or RFC 4733 for peers that negotiate telephone-event.

## Checks

```sh
nix develop
make test
make check
```

`make test` runs race-enabled SIP/audio/media tests plus focused application
lifecycle and storage tests. The suite includes actual loopback SIP signaling
and independent SIPp scenarios, bidirectional synthetic PCM over UDP, codec
vectors, packet impairment, ICE/TURN, SRTP authentication/replay rejection,
independent Pion DTLS peers, TLS trust and account isolation. It never
requires a real PBX or microphone. `make check` builds the frontend, runs Go vet
and validates the Nix package. No automated UI test suite is added.

Independent Baresip tests additionally check all four codecs in both offer modes,
decoded duplex tones, DTMF, hold/BYE, incoming calls, REFER/Replaces, MESSAGE,
presence, conference mix-minus and repeated-call cleanup. These opt-in fixtures
and the separately requested one-hour soak are documented in
[integration/baresip](integration/baresip/README.md).

Native backend acceptance uses private PulseAudio and PipeWire-Pulse daemons with
virtual sinks, plus an isolated ALSA null PCM:

```sh
VOIPER_TEST_NATIVE_AUDIO=1 nix develop path:. --command go test -race -tags integration -run 'Test(PulseSharedDuplex|PipeWireSharedDuplex|ALSA)' -count=1 -timeout=60s -v ./pkg/audio
```

The tests measure simultaneous Voiper and independent `pacat` tones, exercise
capture/playback/duplex lifecycle, detect daemon loss and close cleanly. They also
check explicit ALSA, automatic fallback and unavailable PCM errors. No host audio
service or physical device is used. A reproduced Pulse shutdown deadlock is fixed
by waking the native main loop through `Stop` before joining it during `Uninit`.

Optional native service acceptance runs against private daemons and null devices:

```sh
VOIPER_TEST_NATIVE_AUDIO=1 go test -race -tags integration,opus,speex,secretservice,webkit2_41 ./pkg/audio -run 'Test(PulseSharedDuplex|PipeWireSharedDuplex|ALSANullLifecycle|ALSAUnavailablePCM)' -timeout 2m
```

These checks verify shared audio, daemon-loss shutdown and ALSA lifecycle without
using physical microphones. Independent Baresip checks, including the completed
one-hour call, are described in [the peer acceptance instructions](integration/baresip/README.md).

See [PLAN.md](PLAN.md) for consolidated evidence and the remaining hardware,
desktop and live-Swyx acceptance gates. No automated UI tests are added.
