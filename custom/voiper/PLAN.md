# Voiper implementation plan

Prepared 2026-10-06 from repository commit `ec533db` **and the current working tree**, including the existing dependency and generated-binding edits.

## Scope and outcome

Build a practical Linux desktop SIP softphone, comparable in purpose to Twinkle, with NixOS as the primary platform and strong SwyxWare/SwyxIt! interoperability. Keep Go, Wails, Svelte, and the current visual direction. Prefer existing protocol, audio, and codec libraries; write small, isolated packages for application-specific behavior and missing functionality.

Implementation was authorized after this plan was written. The status below records the current implementation; the repository analysis in section 1 is the original baseline, retained to explain the changes. Unchecked items remain open, including partial implementations whose full acceptance criteria have not been met.

The end result must support ordinary SIP independently of Swyx. Swyx capabilities should be detected per account, activated only when actually supported, and exposed with manual overrides. A Swyx-specific failure must not disable ordinary calling.

Use these priorities:

- **First usable milestone:** install through Nix, configure an account and devices, register, make and receive a two-way audio call, send DTMF, mute, hold/resume, and hang up reliably.
- **Complete daily-use client:** multiple calls/accounts, transfer, conferencing, voicemail indication, contacts/history, messaging/presence where supported, diagnostics, and reliable recovery.
- **Swyx completion:** verify the target deployment's presence, messaging, and directory integrations against a real Swyx client/server pair. Do not substitute guesses or generic SIP success for Swyx compatibility.

Video, fax, screen sharing, a PBX/server implementation, mobile clients, and a general plugin marketplace are outside this plan. Audio conferences and the requested Swyx messaging remain in scope.

## Implementation status — 2026-10-07

The Linux audio softphone is implemented across the following workstreams. This
status describes the current code and measured evidence; target hardware and
proprietary Swyx acceptance remain explicit gates. Detailed checkboxes below
separate implemented behavior from broader deployment acceptance.

| Workstream | Current implementation | Remaining boundary |
| --- | --- | --- |
| Instructions | AGENTS and three validated local skills require readable code, isolated protocols, explicit ownership and proportionate tests | Preserve uncertain behavior during cleanup; no automated UI tests |
| Toolchain / Nix | Go 1.27.1, Wails 2.16.0, Node 24.21.0, pnpm 12.9.0, Svelte 5.57.2/Vite 8.3.3; locked derivation/devshell, GTK/WebKit, Opus, SpeexDSP, libsecret, Pulse/ALSA and independent peer tools | x86_64-linux; locked package checks pass; physical desktop acceptance tracked below |
| SIP | UDP/TCP/verified TLS, digest registration/refresh/recovery, bounded SRV/A/AAAA registrar failover, PRACK per early fork, reliable delayed offers, early/established UPDATE, session timers, cancellation/fork cleanup, re-INVITE, INFO DTMF, REFER/Replaces, recovering subscriptions, PIDF publication and MESSAGE | No NAPTR transport selection or full RFC 5626 flow negotiation; incoming early offer cases and ICE/DTLS branch changes retain documented restrictions |
| Media | RTP/RTCP/SRTP, Opus/G.722/G.711, packet/loss/jitter/RTT statistics, SDES and fingerprint-verified DTLS-SRTP, ICE/STUN/TURN, ICE-lite and one pending transport-only ICE restart | No trickle ICE or mid-call DTLS association/identity replacement; real deployment NAT paths need acceptance |
| Native audio | PulseAudio/PipeWire-Pulse preferred, explicit/automatic ALSA fallback, bounded PCM, levels/gain, SpeexDSP, bounded recovery, fixed device PCM across codec changes, two/three-leg mix-minus conference and private WAV recording | Virtual native-device acceptance passes; physical latency, acoustic echo, Bluetooth and hotplug behavior remain unmeasured |
| Phone / UI | Multiple accounts/calls, asynchronous dial/answer setup, auto-hold, DND, DTMF, blind/attended transfer final-success cleanup, conferences, recording, forwarding/feature codes, statistics, state-safe message views and reviewed import previews | PBX features require provider support; full manual accessibility and everyday workflow acceptance remain separate |
| Desktop | Notifications/actions, tray, safe quit/background behavior, autostart/auto-enable, wallet, URI activation, suspend/network recovery and opt-in portal shortcuts | Packaged GUI launch/calling/quit passes; native portal consent and session support vary |
| Contacts / persistence | Multi-number contacts, CSV field mapping and atomic CSV/vCard import/export, favorites/history/messages, retention, directory DNS/root-DSE discovery, verified LDAP/LDAPS with optional CA, cached synchronization, local edit/delete/conflict preservation and restore, wallet/session credentials | Proprietary directory discovery/API and live Swyx LDAP access remain deployment-specific |
| Swyx Classic | Isolated, fixture-backed reception of Twinkle's PIDF userstatus extension; selectable standard/dialog/Swyx/disabled presence and standard SIP messaging fallback | No live Swyx server; custom status publication and proprietary Messenger authentication/wire contract remain unverified and unimplemented |

### Verification and release record

- Dark-brand and interaction follow-up: darken primary actions to the icon’s deep violet and soften tinted surfaces. Extract Sidebar with a neutral shared hover highlight that slides in 120ms, a quiet active marker, compact custom labels and reduced-motion support. Keep Bits Select keyboard/ARIA behavior while replacing its viewport scrolling with local scrollTop adjustments, fixed popup positioning and opacity-only animation to avoid page-scroll/positioning feedback. Isolated WebKit checks covered a 26-account list, Home/End navigation, selection, hovering a popup on a scrolled settings page and compact sidebar motion. Frontend and locked Nix package builds passed, including native race suites and Go vet.
- Tailwind migration: move page layouts, responsive rules, typography and control states into Svelte utility classes, with shared control class lists in web/src/ui.js. Retain CSS only for theme/base setup, browser-specific checkbox/range/meter/disclosure drawing and animation/reduced-motion rules. Preserve slate surfaces and purple branding; frontend build and isolated WebKit checks covered overview at 1440/480 pixels, account-menu keyboard selection, settings checkboxes, account forms, Contacts and Ctrl+K dialer focus. Manual navigation also exposed an existing unset optional-value error in Dropdown; removing its bindable fallback lets new contacts render with no preferred account. Final locked Nix package build passed, including native race suites and Go vet.
- Gray-palette refinement: replace warm charcoal backgrounds with cooler slate surfaces, darker input fields and clearer panel borders while retaining Voiper purple accents. Main and muted text on cards have 11.4:1 and 6.36:1 contrast respectively. Frontend build, manual desktop visual review and locked Nix package build passed, including native race suites and Go vet.
- Voiper brand theme: use the artwork’s #763BAA violet for primary actions, lighter lavender for focus and icons, muted purple selection surfaces and subtly warm charcoal panels. Shared CSS tokens cover buttons, switches, checkboxes, sliders, navigation, dropdown highlights, messages and the calling workspace; semantic success/error colors remain distinct. Manual WebKit review covered the overview, selected account menu and enabled switch. Main text/button color pairs exceed 4.5:1 contrast. Frontend and locked Nix package builds passed, including native race suites and Go vet.
- Account-menu stability follow-up: account selectors use a shared list of names, updated only when the account list changes. Registration status stays in Accounts and diagnostics rather than changing dropdown labels during retries. An isolated WebKit check kept the menu open across repeated local SIP 200/503 registration responses, then verified mouse and keyboard selection. Frontend production build and locked Nix package build passed, including native race suites and Go vet.
- Calling workspace revision: replace top tabs with a labelled left sidebar that collapses to accessible icons on narrow windows. Overview now combines the dialer and active calls with a compact recent-people list; full phonebook search and presence watches remain in Contacts. Add consistent navigation, call and audio-test icons. Require a separate pointer press on a dropdown option so releasing the opening click cannot accidentally select it; initialize the outgoing account once so refreshes preserve an intentionally empty choice. Isolated WebKit checks at 480, 1024 and 1440 pixels verified layout, recent-person selection, mouse/keyboard account selection, opening-click release protection and empty-selection persistence. Frontend production build and final locked Nix package build passed, including native race suites and Go vet.
- UI revision after user review: restored the original Voiper artwork and compact top navigation, replaced the idle and in-call number grids with keyboard input, standardized form controls and spacing, grouped advanced account options, and paired audio device selectors with explicit five-second tests and measured results. Manual WebKit checks at 1024 and 640 pixels covered navigation, device selection, microphone test progress and silence results, speaker confirmation, saved sound settings and the account editor. Keyboard dialing connected an Opus call to Baresip; typing `5` and Enter in the phone-menu field produced peer-confirmed SIP INFO DTMF. Active calls appear above the new-call form. A follow-up replaces native checkbox/radio and slider appearance with explicit CSS. The rebuilt WebKit app was checked for checked/unchecked/disabled checkbox rendering, label clicks, Space toggling and Tab focus; package checks passed.
- Resize and scrolling follow-up: explicitly enable Linux WebKit on-demand hardware acceleration (the installed Wails version otherwise defaults to Never). Preserve unchanged account/presence/audio state and message collections rather than replacing them on each poll; active-call updates remain live, and contact freshness uses an explicit update timestamp instead of an idle per-second age label. Remove the shell width cap, expand grid-contained settings, support 480×400 windows and compact short-window headers. Manual isolated WebKit checks covered repeated resizing between 480×400 and 1920×1080 and scrolling the overview/settings. Hardware GPU performance remains unverified in the software-rendered test display. Frontend and locked Nix package builds passed, including native race suites and Go vet.
- UI polish: diagnostics moved from the primary navigation and page header into a compact bottom status bar with live-panel, logs and export icons. The nonmodal panel supports Escape and focus return; duplicate account-status blocks were removed and panel edges/flat contact actions refined. Overview now offers explicit contact watches and shows presence source; own-status controls are labeled Your status & voicemail. README documents the distinction between received Swyx status, standard SIP publication and local DND. Manual WebKit checks covered normal/narrow layouts, panel dismissal/focus return and the logs shortcut; frontend and locked package race/vet checks passed.
- Overview and audio-test responsiveness: Overview is the default tab, combining the searchable/paged phonebook, presence indicators, contact calling and existing call controls. Audio feedback no longer forces scrolling or animates a painted gradient; only relevant audio controls are disabled, leaving the fieldset scrollable. Manual isolated WebKit checks covered a 65-contact phonebook, search, number selection and paging; microphone and speaker tests completed against a private PulseAudio null sink while wheel scrolling remained responsive. This does not establish performance on the user’s physical audio/GPU setup. The final frontend and locked Nix package builds passed, including native race suites and Go vet.
- Account controls and live diagnostics: manual registration retry now reuses the enabled SIP client instead of replacing it, available in Accounts and the live diagnostics panel. The panel can stay open on other screens and shows registration errors, call quality and recent logs. Dropdown and panel micro animations respect reduced motion. Focused race tests cover explicit retry after a 403 failure, recovery after a 503 failure and renewal of registered accounts. An isolated WebKit check against a local SIP fixture confirmed visible 403 errors, successful button-triggered registration and repeated renewal at normal/narrow widths. Frontend build and the locked Nix package race/vet checks passed.
- Visual refinement: neutral graphite surfaces, restrained blue accents, compact tab navigation, sharper six-pixel corners, panel/control shadows, consistent typography and styled disclosure indicators. Do Not Disturb uses a Bits UI switch with explicit checked/focus states. Manual isolated WebKit checks covered phone/settings at 1024 and 640 pixels, dropdown rendering and switch mouse/keyboard operation. The final locked package build passed its native race suites and Go vet.
- Dropdown follow-up: replaced every native select with a shared Bits UI component and explicitly styled popup/options. Typed boolean/numeric values, device-change callbacks and disabled states are preserved. Manual Linux WebKit checks covered opening, keyboard selection, typeahead, Escape cancellation, Tab focus, outside dismissal, popup placement at 1024/640 pixels and persisted numeric preferences. Frontend production build passed without warnings; the locked Nix package build and its native race/vet checks passed.
- An existing ordering race in `TestPublicationRemovalStopsRefresh` appeared during the UI package rebuild. Thirty focused race runs with a temporary logging overlay produced 26 passes and four failures: each failure consumed a valid registration-triggered refresh before the correct `Expires: 0` withdrawal. The assertion assumes withdrawal is the next queued request. The final package rebuild passed. No backend or test code was changed during this UI revision; this test still needs to tolerate preceding refreshes while rejecting any refresh after withdrawal.
- Race-enabled protocol/media/application tests cover cancellation, credential and account isolation, forked delayed negotiation, early UPDATE replacement, DNS failover, transfer completion, SQLite migration/import, directory refresh ownership, secure LDAP and isolated desktop D-Bus services. SIPp supplies separate signaling fixtures.
- Media evidence includes synthetic duplex PCM, codec vectors, loss/reorder cases, local ICE/TURN relays and restarts, SRTP authentication/replay rejection, and an independent Pion DTLS client/server validating fingerprints, exported keys, cancellation and closure. Recording and conference tests preserve device PCM across codec changes.
- Independent Baresip 4.11.0 checks decode audio both ways for PCMU, PCMA, G.722 and Opus with initial and delayed offers, plus hold/resume, DTMF and BYE. The combined opt-in suite also passed incoming calls, MESSAGE/presence, REFER/Replaces signaling and measured mix-minus conferences. Thirty client/call cycles retained three goroutines and nine descriptors; after transaction expiry, heap was below its warmup value. See [peer acceptance](integration/baresip/README.md). The separate one-hour PCMA run also passed with zero reported packet loss and stable resource counts; it is not inferred from shorter tests.
- Native acceptance passed repeatedly under the race detector with private PulseAudio 17 and PipeWire 1.6.9/WirePlumber 0.5.18 daemons: Voiper 700 Hz and independent pacat 400 Hz tones both measure about 4000 amplitude in duplex capture. Capture/playback close, daemon-loss health and shutdown also pass. This exposed and fixed a Pulse teardown deadlock: Stop wakes the native main loop before Uninit joins its thread. Isolated ALSA null PCM proves explicit/automatic fallback, callbacks, repeated close and invalid-PCM errors, not hardware timing or mixing.
- The full audio race suite, native fixture vet/Staticcheck and latest frontend production build pass. Final full backend/vet/Staticcheck, dependency consistency and locked Nix package checks pass. The final independent Baresip suite passes after media changes (73.787 s media, 1.292 s signaling). The Go advisory scan found no reachable known vulnerabilities; one advisory affected an unused required module. The existing local skills passed their validators.
- Current UI review fixes prevent a completed message operation from changing a newer account/conversation or erasing its draft, prevent stale import previews from approving different input, and make conference eligibility explicit. No automated UI tests were introduced.

Final verification for the current tree:

- [x] Final locked Nix flake check and package build: all sandboxed native race suites and Go vet passed; `result` points to `/nix/store/ibli0ivm004pgpprv864x1hh0j3ll33j-voiper-0.1.0`, reporting `v0.1.0`.
- [x] Final packaged manual GUI check outside the devshell, using private XDG/D-Bus/Xvfb and PulseAudio services: account creation and registration, a 517-second Opus call to Baresip, mute, hold/resume, peer-confirmed INFO digit `5`, live statistics, hangup, persisted Recent calls and normal Quit (exit 0). The output monitor measured the peer's 400 Hz tone at amplitude 8191 in signed 16-bit PCM. Earlier manual checks covered CSV field mapping/import, directory profile persistence and narrow-window layout. These virtual-device checks do not establish physical acoustic quality or live desktop portal support.
- [x] One-hour Baresip PCMA duplex call (18:17:38–19:17:38 UTC): zero reported packet loss, 13 goroutines and 12 descriptors throughout; normal BYE/cleanup.
- [ ] Physical NixOS microphone/headset, acoustic quality and hotplug matrix.
- [ ] Live target SwyxWare/Classic calls, status, directory and demonstrated messaging provider.

The remaining protocol boundaries are documented in [pkg/sip](pkg/sip/README.md)
and [pkg/media](pkg/media/README.md). A failed ICE restart retains the old local
transport, but cannot guarantee that the peer retains its old path. Reliable
delayed offers maintain at most sixteen candidate media sessions, select one
final fork and keep microphone capture closed until connection. Early UPDATE
replacement is committed only after successful negotiation. Unsupported exchanges
fail explicitly rather than silently changing security or replaying a possibly
completed operation.

## 1. Original repository baseline

These were findings from the planning-only analysis before implementation. They no longer describe the new SIP/media/application packages.

| Area | Evidence in the original tree | Consequence for implementation |
| --- | --- | --- |
| Desktop framework | `cmd/voiper/app/root.go`, `cmd/voiper/wails.json`, `web/embed.go` | Wails v2 application embedding a Svelte/Vite frontend; retain this stack. |
| Dependencies | Working `go.mod` declares Go 1.25.0 and Wails 2.16.0; `web/package.json` uses Svelte 5.22.6, Vite 6.2.1, Tailwind 4.0.12 ranges and npm | Migration has partly started in the working tree. Preserve it, reconcile versions, and regenerate bindings consistently. |
| Packaging | No flake or Nix derivation; Makefile creates a dummy embedded asset; Wails config runs npm | Build real frontend assets before Go packaging and replace npm commands with pnpm. |
| SIP paths | Active application uses `internal/sip`; `cmd/voiper/app/sip.go` contains a separate sipgo registration prototype | Select one implementation. Do not maintain two SIP transaction stacks. |
| Registration | `internal/sip/sip.go:47` uses fixed addresses and credentials, ignores most account fields, starts an unbounded-lifetime status goroutine | Replace prototype wiring with account-driven configuration, explicit lifecycle, and registration state. |
| Account activation | `cmd/voiper/app/app.go:73` holds both application locks while registering with `context.Background()` | An unresponsive server can leave account activation pending and other work waiting on those locks. Keep network work outside application locks and give it cancellation/deadlines. |
| Shutdown | `Client.Close` does not own/close the locally created multiplexer; TCP reader and accept loops lack terminal exit paths | Define ownership and close sockets before waiting for workers. Exercise disconnect, shutdown, and repeated account switching. |
| Framing and routing | `request/request.go`, `response/response.go`, TCP multiplexer, UDP sender/receiver | Parsers depend on read boundaries; TCP bodies use a single `Read`; UDP response routing ignores the received source address; streaming serialization can split a message across UDP writes. These need replacement or regression coverage before interoperability work. |
| Concurrent response delivery | Multiplexers look up a response channel, release the map lock, then send; `StopCall` can close that channel | A send/close race is possible by inspection. Eliminate ambiguous channel ownership and blocking sends during shutdown. |
| Authentication and compatibility | `internal/sip/auth/auth.go` supports only MD5 without qop; REGISTER ignores server expiry; SIP README deliberately excludes combined header values | Current simplifications exclude real peers. Reuse a mature stack and verify required wire behavior. Simplicity cannot mean dropping interoperability requirements. |
| Media | No audio device, SDP negotiation, RTP/RTCP session, codec, or mixer implementation | Build these capabilities in explicit stages. |
| UI | Account editing and logs exist; `Phone.svelte` creates 10,000 random contacts; call controls have no backend actions | Replace mock state with actual account/call/contact state before adding polish. |
| UI lifetime and memory | `App.svelte:17` appends logs without a retention limit; event subscriptions have no component teardown | Bound storage and event traffic; clean up listeners; never forward media frames through Wails. |
| Configuration | TOML and optional AES-GCM/PBKDF2 storage already exist; default base directory is `.`; writes use `os.Create`; account names are joined into paths | Preserve import compatibility, move runtime data to XDG paths, restrict paths/permissions, and use atomic writes. |
| Tests and instructions | No repository test files found; `AGENTS.md` repeats guidance and forbids UI tests; three local skills contain broader generic testing advice | Align instructions first, then add small protocol/audio tests alongside implementation. |

Do not distribute the existing personal configuration files as package assets or copy their endpoints into fixtures. Preserve user edits to `go.mod`, `go.sum`, generated bindings, and the instruction files during execution.

## 2. Intended structure and key decisions

Use one desktop process. The frontend presents state and sends commands; Go owns calls, network sessions, devices, credentials, persistence, and compatibility decisions.

```text
Svelte UI <-> thin Wails bindings <-> internal/phone
                                      |-- pkg/sip -> sipgo
                                      |-- pkg/audio -> Linux devices and DSP
                                      |-- pkg/rtp -> Pion RTP/RTCP/SRTP
                                      |-- pkg/codec -> established codecs
                                      |-- internal/store + internal/directory
                                      `-- internal/compat/swyx
                                            |-- generic SIP extension hooks
                                            `-- authenticated service clients, if needed
```

These are responsibility boundaries, not a requirement to introduce an interface or subpackage for every operation. Create packages as their functionality lands. Keep a single Go module. Move the reusable SIP work to `pkg/sip`; it must not import application configuration, Wails, or Swyx code. Apply the same rule to audio, media transport, and codecs.

Recommended defaults:

| Decision | Direction and reason |
| --- | --- |
| SIP implementation | Finish Voiper's SIP package using maintained `sipgo` transport/transaction/dialog facilities where adequate. Keep Voiper's account and call API small. Retire both current prototypes after equivalent behavior is covered. First validate incoming calls, cancellation, registration, and extension hooks in a small spike. [sipgo project](https://github.com/emiago/sipgo) |
| Audio devices | Start with `malgo`/miniaudio, explicitly preferring PulseAudio, then ALSA. PulseAudio covers a real PulseAudio server and PipeWire's PulseAudio service. Do not rely on a library's implicit backend order. [malgo](https://github.com/gen2brain/malgo), [miniaudio manual](https://miniaud.io/docs/manual/index.html), [PipeWire Pulse protocol](https://docs.pipewire.org/page_module_protocol_pulse.html) |
| Media transport | RTP over UDP with RTCP; SRTP when negotiated. Reuse Pion's packet/protection libraries and implement only Voiper's session lifecycle, pacing, buffering, and statistics. No custom wire transport. [RTP](https://github.com/pion/rtp), [SRTP](https://github.com/pion/srtp) |
| Codecs | Opus for preferred high quality, G.722 for wideband PBX compatibility, G.711 PCMA/PCMU for broad fallback. Negotiate the intersection per call; never assume Swyx supports Opus. |
| Opus implementation | Prefer reference `libopus` with an existing maintained Go binding; `hraban/opus` is a candidate to validate. A tiny private cgo binding is a fallback if existing bindings cannot meet required controls/packaging. Do not implement Opus from scratch. [libopus](https://www.opus-codec.org/downloads/), [Go binding](https://github.com/hraban/opus) |
| SDP | Reuse a parser such as Pion SDP and implement a focused audio offer/answer policy. Parsing alone is not negotiation. [Pion SDP](https://github.com/pion/sdp) |
| Vendor integration | Compile a small Swyx adapter into the app and register only the handlers it needs. No Go dynamic-plugin loader or generic middleware framework. Presence, messaging, and directory capabilities remain independently selectable. |
| Storage | Keep account/settings TOML compatibility. Use a single local SQLite store for contacts, call history, and message history once those features land; use direct queries and small migrations, no ORM/repository framework. |

If the sipgo spike exposes a concrete missing behavior, implement the smallest adapter or upstream fix. Continue the handwritten wire stack only with a documented reason and tests covering its framing, transactions, routing, and shutdown; it is a substantially larger route to a usable client.

## 3. Foundation A — align project instructions

**Files:** `AGENTS.md`, `skills/clean-code/SKILL.md`, `skills/architecture-review/SKILL.md`, `skills/verify-change/SKILL.md`; update their UI metadata only if descriptions change.

- [x] Consolidate `AGENTS.md` into one short set of rules. Keep its emphasis on readable concrete code, focused functions, sparse comments, real boundaries, and avoiding premature abstraction.
- [x] State the project direction: Linux/NixOS desktop softphone; generic SIP core; isolated audio/codec packages; Swyx behavior in a dedicated adapter; Go owns network/media state.
- [x] Preserve the user's testing policy: simple unit and end-to-end tests for SIP and audio/media, no automated UI tests, and other backend tests only for an identified need. Tests must be as readable as production code.
- [x] Replace the assertion that simple code cannot fail with an actionable rule: keep application orchestration simple; add a backend test only for a concrete risk such as cancellation, credential persistence, migration, or cross-call state isolation.
- [x] Replace “unless they conflict with this skill” in `AGENTS.md` with “unless they conflict with these project instructions.”
- [x] In `clean-code`, explicitly discourage interfaces without a real boundary/test seam, trivial wrappers, single-caller generic helpers, and extraction solely to remove a few repeated lines. Prefer consolidating the current per-header fragmentation where it actually simplifies maintenance. Preserve protocol correctness while simplifying.
- [x] In `architecture-review`, focus on concrete ownership: account, SIP dialog, media session, device, and UI events. Review vendor isolation, shutdown, bounded queues, and blocking operations. Do not recommend layers, services, or a plugin framework without a demonstrated need.
- [x] In `verify-change`, defer test additions to `AGENTS.md`; use simple protocol/audio fixtures, loopback peers, and manual UI verification. Report skipped hardware/server checks accurately. Never create a UI automation suite as a side effect of this plan.
- [x] Keep shared policy in `AGENTS.md`; skills should refer to applicable repository instructions rather than repeat Linux/Swyx-specific rules as universal rules for unrelated projects. Validate all three skill files with the skill-creator validator.

**Done when:** the instructions agree about scope and testing, remain short enough to use, and give no reason to overengineer a straightforward change. Implemented and all three skills passed the skill-creator validator.

## 4. Foundation B — current toolchain, pnpm, and NixOS packaging

### Version baseline

The selected compatible versions are pinned in `go.mod`, `web/package.json`,
`web/pnpm-lock.yaml` and `flake.lock`. This is the implementation baseline, not a
claim that every upstream transitive dependency uses its newest release.

| Component | Selected version |
| --- | --- |
| Go | 1.27.1, with automatic toolchain downloads disabled |
| Node.js / pnpm | 24.21.0 / 12.9.0 |
| Wails | Stable v2 API, module and CLI 2.16.0 |
| Svelte / Vite / Svelte plugin | 5.57.2 / 8.3.3 / 7.3.1 |
| Tailwind / Vite plugin | 4.3.3 / 4.3.3 |
| sipgo / malgo | 1.5.0 / 0.11.26 |
| Pion DTLS / ICE / SRTP | v3.1.9 / v4.4.6 / v3.1.3 |

Other direct versions and checksums are recorded in the Go module files. Wails v3
is not required for this application. Review future upgrades for compatibility,
regenerate bindings with the matching CLI, and use `govulncheck` for advisories.

### Migration order

- [x] Record the existing working changes and baseline failures without overwriting them.
- [x] Add a minimal pinned Nix development environment, then update Go/Wails together and regenerate `web/wailsjs` using the same Wails version as the Go module. Do not hand-edit generated runtime files.
- [x] Replace npm with pnpm and a frozen `web/pnpm-lock.yaml`; update the frontend packages as a compatible group and pin `packageManager`. The obsolete npm lock/checksum files are removed.
- [x] Change `frontend:install`, `frontend:build`, and `frontend:dev:watcher` in `cmd/voiper/wails.json` to the pinned pnpm commands. Use frozen lockfiles for repeatable installs. Inspect any pnpm build-script approvals needed by native frontend dependencies and explicitly allow only those required.
- [x] Update Makefile/documented commands; remove obsolete npm checksum artifacts when confirmed unused. Fix ignore rules for `web/dist`, pnpm artifacts, and Nix results. Preserve assets needed for embedding.
- [x] Build the application shell and inspect its rendering manually. Prior isolated native checks passed; final verification of the current packaged tree is tracked above.

### Flake and derivation

**Planned files:** `flake.nix`, `flake.lock`, a small `nix/package.nix` if it keeps the flake readable, and a desktop entry/icon installation step.

- [x] Pin one `nixos-unstable` revision and the selected toolchain in `flake.lock`, targeting `x86_64-linux`. Stable NixOS physical-desktop acceptance and any future `aarch64-linux` support remain separate gates.
- [x] Expose `packages.<system>.default`/`voiper`, `apps.<system>.default`, `devShells.<system>.default`, and useful `checks.<system>`. Keep outputs explicit; no flake framework is needed.
- [x] Build the frontend as a separate derivation using the pinned pnpm, `fetchPnpmDeps`, and its compatible configuration hook. Fetch dependencies with a fixed hash, then build offline with a frozen lockfile. Match the helper's supported fetcher format to the pinned pnpm major; do not copy an obsolete example blindly. [Nixpkgs pnpm packaging](https://nixos.org/manual/nixpkgs/stable/#javascript-pnpm)
- [x] Build the application with the chosen Go builder, a real `vendorHash`, and CGO enabled. Copy the built frontend into `web/dist` before the embedding step. Ensure dependency vendoring/binding generation also sees any required embed inputs. Use the pinned Wails production build procedure and prevent it from reinstalling/rebuilding the frontend over the network.
- [x] For Wails v2, supply GTK3 and WebKitGTK 4.1 and its `webkit2_41` build tag; verify the selected v2 release's requirements. If v3 is separately adopted later, revise the native stack accordingly rather than mixing GTK generations. [Wails Linux installation](https://wails.io/docs/gettingstarted/installation/)
- [x] Include runtime dependencies for PulseAudio client libraries, ALSA, chosen codecs/DSP, GTK schemas, and WebKit helpers. Use appropriate GTK wrapping and explicit runtime paths. Check libraries loaded with `dlopen`: successful linking does not prove miniaudio can find `libpulse` or `libasound` on NixOS.
- [x] Filter personal configs, Git metadata, caches and captures from package build inputs; install the app and required assets/licenses/desktop metadata. Node/pnpm/Wails CLI remain build tools. A flake source snapshot may still enter the Nix store: real credentials must stay outside the checkout.
- [x] Install a desktop launcher with icon, correct application identity, and `sip:`/`sips:` URI handling once the app supports it. URI activation should populate/confirm a call, not silently dial. Use XDG configuration/data/state locations at runtime.
- [x] Package without changing host audio services or firewall rules. Prefer PipeWire with `pipewire-pulse`; media uses allocated UDP sockets or negotiated ICE/TURN. No unneeded host configuration module is installed.

### Development shell

Include the same Go, Node, pnpm, Wails CLI, C toolchain, `pkg-config`, native GUI/audio/codec headers, plus `gopls`, `gofumpt`, `staticcheck`, `govulncheck`, and a Nix formatter. Provide SIPp, a local Asterisk/baresip test option, and audio inspection tools (`pactl`, PipeWire tools, ALSA utilities) either in the main shell or a small named test shell if their footprint is large. Packet inspection tools can be optional. Do not auto-start daemons, install packages, download toolchains, or touch user audio settings from `shellHook`.

Pin Go explicitly and prevent `GOTOOLCHAIN` from downloading another compiler during the sandboxed build. Ensure pnpm does not fetch an alternate package-manager binary inside the derivation. GUI development must inherit the user's display, D-Bus, and audio session; hardware tests run in that session, not inside a pure build sandbox.

**Done when:** a clean checkout can enter `nix develop`, install the locked frontend dependencies, run the app in development, and produce `nix build .#voiper` with sandboxing enabled. The packaged app launches outside the dev shell on Wayland and X11. `nix flake check` covers package construction and headless checks without requiring a microphone, external PBX, or network access during tests.

## 5. Step 0 — usable Linux audio devices

**Primary files:** new `pkg/audio`; small audio settings bindings/UI after the device API works.

- [x] Prove native duplex through malgo with private PulseAudio and PipeWire-Pulse daemons, shared independent-client playback, explicit ALSA null PCM and automatic fallback. The backend is Pulse protocol, not a native PipeWire API; unavailable services/devices return errors.
- [x] Enumerate microphone, playback, and optional separate ringer devices. Support “follow system default” and an explicit device choice; preserve meaningful device identifiers and gracefully handle a saved device disappearing.
- [x] Implement independent input/output gain, mute, level meters, speaker test, and an explicit microphone test. Start capture only for a call or a user-started test. Release it when finished.
- [x] Keep callbacks short: copy through bounded preallocated buffers; no logging, disk I/O, network operations, blocking channel sends, or Wails calls in the device callback. Perform encoding/network work outside it.
- [x] Use explicit mono signed 16-bit PCM and 20 ms processing, with SpeexDSP conversion between the negotiated codec rate and a stable per-call device rate. Keep capture, playback and reference buffers bounded and owned by one producer/consumer.
- [ ] Handle unplugging, changing defaults, suspended devices, Bluetooth headset profile changes, and audio-service restart. Avoid silently switching an active confidential call from headphones to speakers; make fallback policy visible.
- [x] PulseAudio/PipeWire playback must share the output with another application. On ALSA, prefer the configured default PCM and allow explicit selection; report exclusive-device conflicts instead of promising universal mixing or rewriting `.asoundrc`.
- [x] Add speakerphone echo cancellation and optional noise suppression using SpeexDSP, with synthetic signal tests. Physical acoustic quality remains an acceptance gate. Compare another maintained implementation only if measured quality is inadequate. Feed a correctly timed playback reference and avoid double-processing when server-side cancellation is active. [DSP reference](https://www.speex.org/docs/manual/speex-manual/node7.html)

**Checks:** small PCM/buffer/lifecycle unit tests and a headless synthetic-source/sink loopback; manual real-device checks on PipeWire-Pulse, PulseAudio, and ALSA-only setups. Test a USB headset unplug/replug, separate mic/output devices, and simultaneous music playback.

**Done when:** the user can select devices, hear a test signal, see microphone levels, and run a sustained full-duplex loopback without hangs or unbounded buffering. Record latency/underrun measurements on the reference machine; do not claim echo quality from a mock test.

## 6. Step 1 — complete the reusable SIP package

**Primary files:** `pkg/sip`, migration away from `internal/sip`, thin application wiring in `cmd/voiper/app` and `internal/phone`.

- [x] Complete the sipgo suitability spike and record the result. Expose concrete account/client/call types with context-aware operations and typed events. The package owns SIP lifecycle, not UI settings or vendor identities.
- [x] Implement account configuration: registrar, address-of-record/domain, auth identity/password, display name, outbound proxy, transport, local bind/interface choice, registration interval, and trust settings. Distinguish a domain from a registrar socket address.
- [x] Support UDP, TCP, and TLS signaling, IPv4/IPv6, DNS host/SRV discovery where relevant, connection reuse, correct Via/Contact/rport handling, and configured proxy routing. Prefer TLS when configured/available under the account policy; never silently downgrade a TLS-required account.
- [x] Registration: 401/407 challenges, qop/nonces, bounded stale-nonce retries, modern digest algorithms where supported, legacy compatibility where required, server-granted expiry, 423 Min-Expires, refresh, unregister, and reconnect with bounded backoff. [Updated SIP digest specification](https://www.rfc-editor.org/rfc/rfc8760)
- [x] Implement UAC and UAS call flow: INVITE, provisional responses, 180/183, ACK, CANCEL, BYE, errors, transaction timers/retransmission, route sets, tags/CSeq, and cleanup. Include incoming calls on an outbound TCP connection.
- [x] Handle CANCEL racing with a successful answer, repeated final responses, forked answers, retransmitted requests, rejected calls, peer disconnects, and late events. These are reasons to reuse a transaction stack, not to add a generic state-machine framework.
- [x] Add re-INVITE/UPDATE for media changes, hold/resume, session refresh, and PRACK/100rel when negotiated. Keep local mute separate from SIP hold. Reject unsupported mandatory extensions correctly and advertise only implemented capabilities.
- [x] Provide narrow extension registration by method/event/content type as actually needed for subscriptions, MESSAGE, and Swyx. Unhandled traffic follows normal SIP responses. Hooks cannot bypass transaction ownership or stall it on UI/network service work.
- [x] Make account activation asynchronous and cancellable. Maintain explicit registering/registered/retrying/failed states; do not hold application locks during network calls. Close all workers/transports on disable, switch, and app shutdown.
- [x] Replace the current SIP README's deliberate interoperability omissions with a truthful supported-feature list. Delete the abandoned registration prototype and unused custom parser/header packages after their replacements work.

**Checks:** focused units for Voiper's policy and lifecycle; small loopback/SIPp scenarios for authentication, expiry, calls, cancellation, fragmentation/coalescing, duplicate packets, and shutdown; race detector on the relevant Go packages. Use real standard peer interoperability to avoid two copies of the same bug validating each other.

**Done when:** register/refresh/unregister and inbound/outbound call signaling work against a local reference PBX and the target Swyx PBX, with no hard-coded production endpoints or leaked workers. If the Swyx system is not available, record that gate as pending and continue generic implementation.

## 7. Step 2 — RTP, RTCP, and media-session ownership

**Primary files:** `pkg/rtp`; call orchestration in `internal/phone`.

- [x] Reuse Pion RTP/RTCP for packet formats. Voiper owns per-call sockets, SSRC/sequence/timestamps, payload mapping, pacing, receive validation, and session start/stop. Support separate RTP/RTCP ports first; enable RTCP mux only when negotiated.
- [x] Keep a bounded jitter buffer with reordering, duplicate rejection, wraparound handling, late-packet policy, and packet-loss concealment integration. Evaluate an existing buffer before writing one; a small isolated implementation is acceptable if existing choices impose unrelated infrastructure.
- [x] Measure sender/receiver reports, loss, jitter, RTT when report data permits, bytes/packets, underruns, and buffer delay. Missing RTCP yields “unavailable,” not an invented zero.
- [x] Bind media addresses/ports independently from the registrar address. Swyx media may travel directly between clients; do not send audio to the SIP server just because it registered the account. The vendor documents RTP/UDP and direct client media. [Swyx network behavior](https://service.swyx.net/hc/en-gb/articles/14020952752540-Which-Ports-are-used-by-Swyx-14)
- [x] Establish LAN/VPN interoperability, configurable advertised addresses, periodic RTCP and ICE/STUN/TURN where negotiated. Optional SDES-SRTP port rebinding accepts only authenticated fresh packets from the signaled IP; unauthenticated plain RTP remains pinned. Reuse Pion's traversal libraries; STUN alone is not a universal NAT solution. [ICE](https://github.com/pion/ice), [TURN](https://github.com/pion/turn)
- [x] Implement SRTP/SRTCP using Pion, SDES over verified TLS, and DTLS-SRTP with SDP fingerprint/setup validation, bounded handshake, exported keys and downgrade rejection. Keep plain RTP explicit and report actual protection only after establishment. Target Swyx security-profile acceptance remains pending.
- [x] Close media promptly on hangup, cancelled setup, account disable, or application exit. Bound work on malformed/unexpected packets. Handle no-audio/one-way-audio timeouts without blocking the UI.

**Checks:** loopback packets and small deterministic loss/reorder/jitter scenarios; sequence/timestamp wrap, payload validation, SRTP authentication/replay rejection, RTCP accounting, and start/stop. Keep fixtures readable; no general network simulator framework.

**Done when:** two endpoints exchange paced media, statistics reflect an intentionally impaired path, and session resources close cleanly. Complete a NAT/relay check before advertising remote-network support.

## 8. Steps 3 and 3.1 — codecs and SDP negotiation

**Primary files:** `pkg/codec`, audio conversion in `pkg/audio`, SDP/call negotiation in `pkg/sip` with media setup owned by `internal/phone`.

### Codec implementation

- [x] First implement G.711 PCMA/PCMU to obtain a simple interoperable vertical call. Evaluate `zaf/g711` rather than recreating the companding code. [G.711 library](https://github.com/zaf/g711)
- [x] Add G.722 through `gotranspile/g722` and Opus through the libopus Go binding. The current voice policy uses up to 32 kbit/s subject to negotiated limits, playback bandwidth constraints, negotiated FEC and decoder PLC. Fresh peer RTCP loss reports now adjust the Opus encoder bitrate and expected packet-loss setting conservatively within the negotiated cap. FEC remains negotiated; DTX is not enabled. [G.722 candidate](https://github.com/gotranspile/g722)
- [x] Give each codec a small frame-oriented contract: supported PCM format, encode/decode, reset/close, and capabilities. Use a small interface because there are real interchangeable codecs; do not introduce a codec registry framework.
- [x] Verify both encoding and decoding, license compatibility, maintained source, Nix builds, known vectors, and behavior on invalid input. Pure-Go Opus is an alternative only after verifying feature coverage and quality; do not assume either that it is decoder-only or that it is equivalent to libopus. [Pion Opus](https://github.com/pion/opus)
- [x] Reuse resampling and DSP implementations. If a required function has no suitable dependency, isolate it with a precise input/output contract and known-vector tests. Avoid writing codecs, cryptography, or signal processing from first principles merely to avoid CGO.

### Per-call negotiation

- [x] Default preference: Opus, G.722, PCMA, PCMU, with per-account overrides. Choose only mutually supported codecs and parameters; an unsupported high-quality codec must fall back cleanly.
- [x] Implement offer/answer for incoming and outgoing calls, dynamic payload mapping, `rtpmap`, codec `fmtp`, `ptime`/`maxptime`, direction attributes, address families, rejected streams, early media, and subsequent offers. Handle delayed offers and reject incompatible media clearly, without leaving a call half-created.
- [x] Keep RTP clock rate separate from PCM rate: Opus RTP uses 48 kHz; G.722 RTP uses 8 kHz although its PCM is wideband 16 kHz. Do not hard-code Opus to one dynamic payload number. [Opus RTP](https://www.rfc-editor.org/rfc/rfc7587), [RTP audio profile](https://www.rfc-editor.org/rfc/rfc3551)
- [x] Use 20 ms mono voice with a bounded adaptive Opus bitrate, negotiated constraints and fresh RTCP loss feedback. Preserve audio continuity on hold/resume, early-media changes and codec renegotiation; expose bitrate and report age. This does not claim bandwidth estimation or DTX.
- [x] Support RFC 4733 telephone events and a configurable SIP INFO DTMF mode; use INFO for Swyx profiles when verified. Vendor documentation describes INFO for third-party SIP endpoints, so test this explicitly rather than assuming RTP telephone events work on every Swyx version. Avoid sending both methods for one keypress. [DTMF RTP](https://www.rfc-editor.org/rfc/rfc4733), [Swyx client manual, third-party SIP devices](https://help.enreach.com/docs/manuals/english/SwyxIt%21_classic.pdf)

**Done when:** demonstrate two-way Opus calling with a compatible peer, wideband G.722 calling where supported by the target Swyx installation, and G.711 fallback. Verify an IVR accepts exactly the intended digits. Show negotiated codec, rate, packetization, and protection honestly in diagnostics.

## 9. Step 4 — finish the phone experience

Build a usable dialer/active-call view as soon as the G.711 vertical slice works; do not wait for all compatibility features. Retain the current design direction but favor stable readable controls over continuous animation.

| Feature group | Required behavior and implementation boundary |
| --- | --- |
| Accounts | Create/edit/enable multiple accounts; choose default outgoing account; see registration and errors per account; retry/cancel without freezing. Do not share credentials or vendor profiles across accounts. |
| Dialing | Search/name/extension/number/SIP URI input, dialpad, paste, redial, speed dials, favorites, incoming caller identity, answer/reject, ringback/early media, duration, and clear termination reasons. Preserve internal extensions and service codes when normalizing numbers. |
| Call controls | Mute, input/output volume, device selection, hold/resume, DTMF, call waiting, multiple call cards, swap calls, busy response, and local do-not-disturb. Make microphone-active state visible. |
| Transfer | Blind transfer and attended transfer with consultation, cancel/back-to-original-call, and progress/failure. Implement REFER/NOTIFY and Replaces flows as supported; do not hang up the original call before the transfer outcome is known. [SIP transfer examples](https://www.rfc-editor.org/rfc/rfc5589) |
| Forwarding and redirection | Distinguish local redirect of a ringing call from persistent PBX forwarding. Offer unconditional/busy/no-answer forwarding through a supported provider feature code/API; show whether it works while the app is offline. Handle received redirects with a bounded policy, not an endless dialing loop. |
| Conferences | Start with a local three-party audio conference using independent SIP legs and mix-minus audio so nobody receives their own voice. Allow add/remove, leave/end, hold, and failure recovery. Add server conference room dialing and participant control only where the PBX provides it. |
| PBX features | Voicemail dial shortcut and message-waiting subscription; busy-lamp/dialog state, directed/group pickup, and park/retrieve where supported. Store server feature codes as account configuration, never universal hard-coded numbers. |
| History | Incoming/outgoing/missed/failed calls, timestamps/duration, account used, redial, contact creation, delete/clear, and bounded retention. Distinguish a missed call from one answered elsewhere when signaling supplies that fact. |
| Contacts | Local/imported/directory contacts, multiple numbers, favorites, search, click to call/message, status with source and age. Step 8 below supplies persistence and discovery. |
| Messaging/presence | Generic SIP baseline plus separate Swyx providers; clear unsupported/offline/error states, unread counts, and notifications. Do not imply server delivery or rich presence solely from registration. |
| Recording | Optional user-started audio recording with visible state, chosen destination, disk-error handling, and retention controls. No background recording by default. |
| Desktop integration | Notifications with call actions where supported, tray/status integration where available, configurable background/close behavior, autostart opt-in, single-instance URI activation, and keyboard shortcuts. Avoid dependence on a tray existing on every desktop. |
| Accessibility | Keyboard navigation, visible focus, labels, contrast, reduced motion, scalable text, and usable compact layouts. Headset buttons/global shortcuts are capability-dependent enhancements; validate on the target desktop rather than promising universal Linux support. |

Model provider capabilities explicitly so unsupported features explain themselves instead of displaying nonfunctional buttons. Do not present local DND as remote presence publication, local forwarding as a persistent server rule, or a local conference as a server conference.

**Done when:** the daily workflows above work in manual acceptance sessions with an independent SIP peer and Swyx where applicable. Unsupported server features are visibly unavailable or use configured alternatives. No dummy contacts, hard-coded identities, or inert call controls remain.

## 10. Step 5 — responsiveness, lifecycle, and call statistics

Apply these constraints from the first call; this phase completes and measures them.

- [x] Keep a simple authoritative call/account model in Go. Give events stable account/call identifiers and enough sequencing to discard stale updates. On view remount/reconnect, fetch a snapshot rather than relying on having seen every event.
- [x] Accept dial/answer setup asynchronously and report progress through snapshots/events; reject duplicate pending activation. Go resolves answer/CANCEL races. Network/device work uses explicit deadlines and ownership outside application locks; native cleanup still reports a bounded pending state if a driver does not return.
- [x] Bound queues, logs, notifications, and history paging. Preserve call-state transitions; coalesce disposable meter/stat updates. Unsubscribe component listeners and remove terminal call state after history is saved.
- [x] Replace random contact generation and JSON-string search with persisted normalized fields and bounded/paged queries. Debounce search where useful; do not introduce a worker or complex state framework without measured need.
- [x] Offer an optional statistics drawer: codec, sample/RTP rates, packetization, transport, actual encryption, local/remote media address, loss, jitter, RTT, bitrate, buffer delay, underruns, and input/output levels. Sample aggregated statistics around once per second; use a modest separate meter cadence. Label estimated/unavailable values.
- [x] Provide a bounded diagnostic log and user-triggered redacted export. Avoid credentials, authentication headers, SRTP keys, and message bodies in routine logs. Never stream full packet contents into the UI by default.
- [x] Recover registration after network changes/resume and retry stopped devices with bounded backoff and visible errors. Keep selected device/backend identity; native daemon-loss shutdown and synthetic replacement lifecycle pass. Physical unplug/replug acceptance remains open.
- [x] Stop accepting commands, cancel owned operations, close media and transports, and attempt bounded dialog/registration cleanup during shutdown. A stalled native operation is reported without freeing resources still in use. Normal native Pulse/ALSA teardown and cancellation regressions pass; broader physical-driver behavior remains an acceptance gate.

**Acceptance targets:** on a recorded reference NixOS machine, command feedback appears within roughly 100 ms even if the remote action takes longer; a one-hour call and repeated call/account/device cycles do not produce steadily growing workers, memory, or queued events; a server timeout leaves navigation and hangup usable. Measure these manually and with small Go instrumentation where needed, without building automated UI tests.

## 11. Step 6 — standard presence and isolated Swyx status

### What is established, and what remains unknown

Public Swyx documentation describes restrictions for ordinary third-party SIP phones, including possible absence of status and pickup. It does not establish a complete proprietary XML presence wire format for the target installation. Treat the user's observed XML exchange as a lead to investigate; XML by itself does not prove a nonstandard protocol, since standard SIP presence also uses XML. [Swyx third-party phone limitations](https://service.swyx.net/hc/en-gb/articles/360001737140-General-information-about-3rd-party-SIP-phones)

Start gathering the target server/client versions and traces during Foundation B, so this discovery does not arrive at the end of implementation. Actual adapter implementation follows the working SIP core.

### Standard baseline

- [x] Implement subscription lifecycle and NOTIFY handling for standard presence, with PIDF parsing; add publication where supported. Separately support dialog/BLF state and message-waiting indications. Standard event subscription, presence, and dialog state are distinct capabilities. [SIP events](https://www.rfc-editor.org/rfc/rfc6665), [presence package](https://www.rfc-editor.org/rfc/rfc3856), [dialog package](https://www.rfc-editor.org/rfc/rfc4235)
- [x] Normalize states into available, away, busy/in-call, DND, offline, and unknown with source/timestamp. Preserve unsupported distinctions as unknown rather than inventing them. A successful OPTIONS response is reachability, not user availability.
- [x] Provide per-account presence mode: Auto, Standard presence, Dialog/BLF, Swyx, or Disabled. Show detected capability and allow overrides. Subscription rejection or expiry should degrade to unknown with bounded retry.

### Swyx discovery and adapter

- [ ] With a controlled test account and server, record native-client login/logout, available/away/DND changes, outgoing/incoming/ringing/held/conference calls, status text, reconnect, and subscription refresh. Record exact SwyxWare/SwyxIt! versions and whether the client is Classic or newer.
- [ ] Identify actual SIP methods, Event/Accept/Content-Type headers, multipart structure, XML root/namespaces, identity fields, timers, request/response pairing, and acknowledgments. Check whether some state instead travels over a service API. Do not invent names such as an undocumented `x-swyx` content type.
- [ ] For encrypted traffic, use supported endpoint/server logging or a controlled lab capture method. Do not disable production TLS to make analysis convenient. Preserve sanitized wire fixtures with expected normalized events; keep credentials and personal messages out of the repository.
- [x] Implement only demonstrated dialects under `internal/compat/swyx`. Use generic SIP hooks and bounded XML parsing; the SIP package must not import Swyx or branch on server names. Unknown versions/messages should fail safely and leave standard handling available.
- [ ] Detect hints from authenticated registration responses and observed capabilities, then confirm the adapter's actual exchange. A Server/User-Agent string is a hint, not authentication or proof that every Swyx feature exists. Cache capability results per account and invalidate on meaningful server/version changes.
- [ ] Validate publishing separately from receiving status; permissions may differ. Fail back according to the user's mode, with clear unsupported status, rather than repeatedly probing every contact.

**Done when:** a fixture-backed adapter and live test reproduce the target client's status changes, the UI allows provider selection, and the same build continues to work on a generic SIP PBX. Without server access/traces, complete standard presence and keep the Swyx compatibility gate explicitly pending.

## 12. Step 7 — messaging that matches the actual Swyx generation

There are at least two relevant cases. The native manuals distinguish older SwyxIt! messaging from the newer Swyx Messenger introduced around SwyxWare 12.10. Swyx's port documentation lists HTTPS authentication/API traffic and WebSocket chat for the newer Messenger, including cloud services. Therefore an implementation based only on SIP MESSAGE cannot be assumed to interoperate with modern Swyx Messenger. [Swyx Messenger manual](https://help.swyx.com/docs/manuals/english/SwyxIt%21_classic.pdf), [Swyx Messenger transports](https://service.swyx.net/hc/en-gb/articles/14020952752540-Which-Ports-are-used-by-Swyx-14)

- [x] Implement standard SIP MESSAGE text messaging with content-type/size limits, proper responses, sender/account mapping, and local history. Distinguish accepted by server from delivered/read; SIP acceptance alone does not establish recipient delivery. [SIP MESSAGE](https://www.rfc-editor.org/rfc/rfc3428)
- [ ] During Swyx discovery, test one-to-one send/receive, offline behavior, UTF-8, conversation identity, delivery/read signals, reconnect, and multiple logged-in clients. Determine whether the target uses legacy SIP messaging, the modern service, or both.
- [ ] Implement demonstrated legacy SIP behavior in the Swyx adapter. If modern Messenger is required, obtain the supported API/authentication contract and validate Linux-compatible login, tokens, refresh, service discovery, and WebSocket events. SIP credentials may not be sufficient for cloud messaging.
- [ ] Use a small application-level messaging provider boundary shared by standard SIP, demonstrated Swyx legacy messaging, and the modern service client. Keep HTTP/WebSocket authentication out of `pkg/sip`; share only the app's normalized message/conversation model.
- [ ] Default to Auto. After positive server/capability detection, select the compatible Swyx provider when it is usable. If additional login is needed, guide the user through it. Allow manual Standard/Swyx/Disabled selection and show the effective provider.
- [x] Do not silently resend a possibly delivered message through a different provider after a timeout. Track pending/sent/failed states and provider message IDs where available; make retry behavior explicit and avoid duplicate delivery across reconnects.
- [x] Add unread counts, notifications, searchable/paged history, and local retention. Add group chat, attachments, typing, or read receipts only when supported by the selected provider; do not fake them over generic SIP.

**Done when:** bidirectional messages interoperate with the actual target Swyx client generation and a standard SIP peer through their respective providers. If the modern service contract/login is unavailable, document the exact dependency and keep that milestone open; basic SIP chat is not completion of the Swyx messaging requirement.

## 13. Step 8 — phonebook, imports, and useful discovery

Ship a functional local phonebook before remote discovery is complete.

- [x] Store contacts with stable IDs, names, multiple numbers/SIP URIs, account/source, favorites, and notes. Preserve extension/service-code semantics; normalize international numbers only when country/account context is known.
- [x] Import/export CSV and vCard with preview, explicit CSV field mapping, duplicate handling, malformed-record reporting and atomic commit. Exact primary addresses identify contacts; optional number merging preserves personal fields. Do not merge by display name or guess telephone country context.
- [x] Build recent correspondents from successful/received calls and messages; offer “save contact.” Keep unverified caller-supplied names distinct from curated or directory identities. Do not scan or dial extension ranges to discover users.
- [ ] Investigate Swyx directory access through supported provisioning/service discovery and authorized LDAP/LDAPS or client APIs. Vendor documentation confirms LDAP/ADLDS is used to expose phonebooks for some certified phones; this establishes a candidate route, not that every server exposes it to this app. [Swyx LDAP phonebook](https://service.swyx.net/hc/en-gb/articles/13860542950940-Swyx-Phonebook-Does-Not-Longer-Work-with-Older-Yealink-Phones-T4x-CP9x0), [provisioning settings](https://help.enreach.com/controlcenter/14.25/web/Swyx/en-US/help/chap_serverconfiguration.06.11.html)
- [x] Accept configured directory endpoint, base DN, identity, and certificate trust when they cannot be discovered. Prefer an ordinary user's read-only access; do not require database/admin credentials. Respect referrals and server permissions.
- [x] Research supported service boundaries before considering the CDS SDK. No verified Linux-compatible proprietary directory contract was found for this target; configured secure LDAP and DNS/root-DSE discovery are implemented. The documented CDS SDK relies on WCF/.NET assemblies, so it is not a drop-in native Go/Linux dependency. Avoid making Windows-only middleware necessary for the softphone. [CDS SDK](https://cdssdk.swyx.engineering/guide/cdsclient-getting-started.html)
- [x] Cache directory results for offline use, refresh incrementally or with bounded paging, preserve local edits separately, and distinguish remote deletions from temporary refresh failure. Show directory source and last refresh.
- [x] If discovery/access is unavailable, local contacts, imports, recent correspondents, and favorites remain fully usable. Display a configuration option rather than spinning forever or inventing a directory endpoint.

**Done when:** import, search, deduplicate, call, edit, and export work; recently encountered addresses can become contacts; an authorized Swyx directory sync works if exposed, otherwise the fallback is complete and its limitation is documented.

## 14. Cross-cutting application work

Implement these with the phases that first need them:

- **Persistence:** migrate existing TOML/encrypted configurations without losing accounts. Use XDG directories, restrictive file permissions, atomic writes, path validation, schema versions, and recoverable data migrations. Prefer a desktop secret service where available, with the existing encrypted-file mechanism as an explicit fallback. Store no credentials in Nix expressions or generated frontend data beyond what editing actually needs.
- **Transport protection:** verify TLS certificates, support configured private CAs, and show signaling and media protection separately. No global insecure-certificate switch as the solution to Swyx compatibility.
- **Input handling:** bound SIP/XML/message sizes and parse remote display names/messages as data. Avoid raw HTML rendering. Keep SIP, directory, and chat credentials scoped to their intended services.
- **Recovery:** account registration, media sessions, message connections, directory refresh, and audio devices need independent cancellation/retry policies. A retry must not place a second call, send a duplicate message, or repeat a transfer unknowingly.
- **Conferencing:** each leg owns codec/RTP state; a small mixer handles rates, levels, clipping, and mix-minus. Changing a device or losing one participant must not tear down unrelated calls.
- **Documentation:** replace the README's hard-coded public test server with reproducible local setup, account/audio onboarding, Nix commands, known Swyx versions, feature support, diagnostic export, and troubleshooting for NAT/DTMF/no-audio.

## 15. Verification and release gates

Follow the revised `AGENTS.md`: simple SIP/audio/media units and end-to-end checks, only necessary extra backend tests, no automated UI test implementation. Build/type/lint checks and manual interaction checks remain useful.

| Layer | Small, meaningful evidence |
| --- | --- |
| SIP | Local scripted peer/SIPp plus an independent PBX: registration, challenges, refresh, calls, CANCEL/answer races, hold, transfer, subscription lifecycle, malformed framing, shutdown. |
| RTP/codecs/audio | Known codec vectors, full-duplex loopback with synthetic PCM, bounded jitter/loss scenarios, packet counters, device/mixer lifecycle, SRTP rejection checks; manual real-device quality. |
| Swyx | Sanitized captured fixtures plus a live native-client comparison for presence, messaging, transfer, DTMF, and directory access. Record server/client versions and provider mode. |
| Application backend | Add focused tests only for risks not adequately covered elsewhere: persistence migration, path constraints, cancellation, event ordering, multi-account/call isolation, or duplicate-message prevention. |
| UI | Manual answer/hangup, timeout, keyboard, device loss, multi-call, compact window, large contact list, message/presence, and statistics sessions on Wayland/X11. |
| Packaging | Clean locked build, `nix flake check`, packaged execution outside dev shell, no host-path dependence, no network fetch during sandbox build, and runtime discovery of dynamically loaded audio libraries. |

Routine verification commands:

```sh
nix develop
pnpm --dir web install --frozen-lockfile
pnpm --dir web build
go test -race ./pkg/...
go vet ./...
nix flake check
nix build .#voiper
nix run .#voiper
```

Run the application-package tests with `make test`. Run protocol end-to-end tests through a documented local target with explicit peer setup, timeouts, and cleanup; they must not contact a real PBX by default. Native GUI checks may need the selected Wails build tag and generated assets. Do not interpret `[no test files]`, skipped integration cases, or a successful build as proof of calling/audio interoperability.

Release acceptance matrix:

- [ ] NixOS + PipeWire/Pulse compatibility + Wayland: installed app, shared audio, all first-call operations.
- [ ] NixOS + PulseAudio + X11: same basic calling/device behavior and coexistence with another app.
- [ ] ALSA-only environment: explicit device selection, duplex audio, and useful device-busy errors.
- [x] Independent Baresip peer: G.711/G.722/Opus, inbound/outbound, DTMF, hold, blind/attended transfer, conference, presence and text MESSAGE. This is local independent-peer evidence, not a deployed PBX certification.
- [ ] Target Swyx version: generic calls, supported wideband codec, INFO DTMF, transfer/conference, observed status dialect, correct messaging provider, and directory or documented local fallback.
- [ ] Network loss, NAT path, resume, wrong password, rejected TLS certificate, unavailable mic, unplugged headset, and PBX restart: bounded recovery with usable UI and no resource accumulation.
- [x] One-hour Baresip PCMA call and thirty complete client/call lifecycles show stable resources and measured audio/statistics behavior; focused account replacement/cancellation tests cover account changes. Physical-device soak remains part of the hardware matrix.

## 16. Execution sequence and unresolved inputs

Treat each item as a reviewable implementation milestone. Update this plan with evidence and remaining limitations as execution progresses.

1. **Instructions and baseline:** Foundation A; preserve current edits; document baseline build issues.
2. **Reproducible shell/build:** Foundation B, pnpm migration, matching Go/Wails bindings, packaged existing UI. Start Swyx version/capture discovery now.
3. **Device proof:** Step 0 device enumeration and duplex loopback; decide the audio backend and DSP boundary.
4. **One SIP stack:** Step 1 registration and core dialog behavior; retire competing prototypes after replacement.
5. **First real call:** minimum Step 2 + G.711/SDP from Steps 3/3.1 + a functional dialer/answer/hangup view. This is the first usable release gate.
6. **Quality and network reliability:** Opus/G.722, buffering, DSP, RTCP/statistics, protection, NAT traversal, and lifecycle recovery.
7. **Daily phone workflows:** Step 4 transfer/multiple calls/conference/voicemail/history; complete Step 5 responsiveness. Add local contacts/history early enough for these workflows.
8. **Presence:** standard baseline and Step 6's captured Swyx dialect, with provider selection and generic fallback.
9. **Messaging:** standard SIP and the target Swyx generation's actual provider, authentication, and delivery semantics.
10. **Directory/import completion:** Step 8 remote access where available, offline cache, import/export, and accumulated contacts.
11. **Release pass:** complete the acceptance matrix, package/runtime checks, manual accessibility/UI review, and support documentation.

Inputs needed before the relevant Swyx/hardware gates, not before starting generic implementation:

| Input | Why it matters | Work that can proceed without it |
| --- | --- | --- |
| SwyxWare version/build, SwyxIt! Classic/new client version, on-premises/SwyxON topology | Determines dialect, authentication, media routing, and available service APIs | Nix, generic SIP/audio, standard presence/chat, local contacts |
| Dedicated test accounts and access to an independent phone/native Swyx client | Required to verify two-way behavior, transfers, conferences, and messaging | Loopback/reference PBX development |
| Sanitized status/message traces or supported API documentation | Proprietary XML and modern Messenger cannot be implemented truthfully from guessed payloads | Generic hooks, normalized state, standard protocols |
| Directory endpoint/access or provisioning details | LDAP/API availability and credentials are deployment-specific | CSV/vCard and local/observed contacts |
| Reference NixOS desktop, mic/headset, and Bluetooth/speakerphone expectations | Enables actual latency, hotplug, mixing, and echo-quality acceptance | Synthetic audio/media tests and package builds |

The ordinary SIP/audio client and the documented Classic status adapter are implemented. Completion of the target-specific Swyx gates requires its server/version and demonstrated messaging/publication contract; no live proprietary interoperability is claimed. Physical-device and final release evidence must be recorded separately from synthetic or independent loopback tests.
