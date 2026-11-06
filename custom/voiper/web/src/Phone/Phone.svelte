<script>
  import { ui } from "../ui.js";
  import Icon from "../components/Icon.svelte";
  import ContactCard from "../Contacts/ContactCard.svelte";
  import CallTargetPicker from "./CallTargetPicker.svelte";
  import { extensionOf, statusStyle } from "../Contacts/presence.js";
  import * as api from "../../wailsjs/go/app/App.js";
  let {
    snapshot,
    account = $bindable(""),
    target = $bindable(""),
    run,
    busy,
    workspace = false,
  } = $props();
  let suggestions = $state([]);
  let contactError = $state("");
  let redialNotice = $state("");

  $effect(() => {
    const query = target.trim();
    let cancelled = false;
    const timer = setTimeout(async () => {
      try {
        const contacts = await api.ContactsPage(query, 0, 8);
        if (!cancelled) {
          suggestions = query ? contacts : workspace ? [] : contacts.filter((c) => c.Favorite);
          contactError = "";
        }
      } catch (error) {
        if (!cancelled) contactError = String(error);
      }
    }, 150);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  });
  let presenceByTarget = $derived(
    new Map(
      (snapshot.Presence || [])
        .filter((p) => p.Account === account && !p.Voicemail)
        .map((p) => [p.Target, p]),
    ),
  );
  let currentAccount = $derived(snapshot.Accounts.find((a) => a.Name === account));
  let accountRegistered = $derived(
    snapshot.Accounts.some((a) => a.Name === account && a.State === "registered"),
  );
  $effect(() => {
    const extensions = suggestions.map(extensionOf).filter(Boolean);
    if (account && accountRegistered && extensions.length)
      api.WatchVisiblePresence(account, extensions).catch((e) => (contactError = String(e)));
  });
  const statRow = "flex justify-between gap-4 py-[0.6rem] border-b border-border";
  const statValue = "m-0 min-w-0 text-right wrap-anywhere";
  let toneNotices = $state({});
  let showStats = $state(false);
  // The open tool of each call: "transfer", "add", "keypad" or "more".
  let panel = $state({});
  let statusNote = $state("");
  let restarting = $state({});
  let restartErrors = $state({});
  let connectedCalls = $derived(snapshot.Calls.filter((call) => call.State === "connected"));
  let hasConference = $derived(
    snapshot.Calls.some((call) => call.Stats?.Conference),
  );
  let callsByID = $derived(new Map(snapshot.Calls.map((call) => [call.ID, call])));

  function togglePanel(id, name) {
    panel[id] = panel[id] === name ? "" : name;
    toneNotices[id] = "";
  }
  // "sip:318@pbx" shows as 318; the full address stays available as a title.
  function remoteName(remote) {
    return (remote || "").replace(/^sips?:/, "").replace(/@.*$/, "") || remote;
  }
  function stateLabel(call) {
    if (call.Held) return "On hold";
    return (
      {
        preparing: "Preparing…",
        calling: "Calling…",
        ringback: "Ringing…",
        ringing: "Incoming call",
        answering: "Answering…",
        connecting: "Connecting…",
        connected: "Connected",
      }[call.State] || call.State
    );
  }
  // Only problems that stop the call's audio reach the call view; other media
  // errors, such as isolated packet loss, are listed in the diagnostics panel.
  function blockingMediaWarning(stats) {
    return stats?.MediaFlow === "no-media" || stats?.MediaFlow === "device-unavailable" ? stats.MediaWarning : "";
  }
  async function sendTone(call, digit) {
    await api.SendDTMF(call.ID, digit);
    toneNotices[call.ID] = `${toneNotices[call.ID] || "Sent "}${digit}`;
  }

  $effect(() => {
    const active = new Set(snapshot.Calls.map((call) => call.ID));
    for (const id of Object.keys(restarting)) {
      if (!active.has(id) && !restarting[id]) {
        delete restarting[id];
        delete restartErrors[id];
      }
    }
  });

  function duration(connected) {
    const seconds = Math.max(
      0,
      Math.floor((Date.now() - new Date(connected).getTime()) / 1000),
    );
    return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
  }

  async function restartPath(id) {
    restarting[id] = true;
    restartErrors[id] = "";
    try {
      await api.RestartMediaPath(id);
    } catch (error) {
      restartErrors[id] = String(error);
    } finally {
      restarting[id] = false;
    }
  }

  function statistics(stats) {
    const flowLabels = {
      waiting: "Starting media",
      closed: "Closed",
      held: "On hold",
      "device-unavailable": "Audio device unavailable",
      securing: "Securing media",
      inactive: "Inactive",
      "no-media": "No RTP flow",
      "missing-outbound": "No outgoing RTP",
      "missing-inbound": "No incoming RTP",
      duplex: "Sending and receiving RTP",
      sending: "Sending RTP",
      receiving: "Receiving RTP",
    };
    const received = stats.PacketsReceived || 0;
    const lost = stats.PacketsLost || 0;
    const loss =
      received + lost
        ? `${((100 * lost) / (received + lost)).toFixed(1)}%`
        : "No packets yet";
    return [
      ["Codec", stats.Codec || "Negotiating"],
      ["Audio flow", flowLabels[stats.MediaFlow] || "Waiting for audio"],
      [
        "Encoder bitrate",
        stats.EncoderBitrate > 0
          ? `${(stats.EncoderBitrate / 1000).toFixed(1)} kbit/s`
          : "Fixed by codec",
      ],
      [
        "Audio rate",
        stats.SampleRate
          ? `${stats.SampleRate / 1000} kHz codec · ${(stats.DeviceSampleRate || stats.SampleRate) / 1000} kHz device`
          : "Waiting for audio",
      ],
      [
        "Encryption",
        stats.Codec
          ? stats.Encrypted
            ? stats.Transport?.startsWith("UDP/TLS/")
              ? "DTLS-SRTP encrypted"
              : "SDES-SRTP encrypted"
            : stats.Transport?.startsWith("UDP/TLS/")
              ? "DTLS handshake pending or closed"
              : "RTP unencrypted"
          : "Negotiating",
      ],
      ["Received packet loss", `${loss} · ${lost} lost`],
      [
        "Peer-reported packet loss",
        stats.ReceiverReportAvailable
          ? `${(stats.RemotePacketLossPercent || 0).toFixed(1)}% · report ${(stats.ReceiverReportAgeSeconds || 0).toFixed(0)} s ago`
          : "Waiting for RTCP reports",
      ],
      ["Jitter", `${(stats.JitterMilliseconds || 0).toFixed(1)} ms`],
      [
        "Round trip",
        stats.RTTMilliseconds > 0
          ? `${stats.RTTMilliseconds.toFixed(1)} ms`
          : "Waiting for RTCP reports",
      ],
      ["Playout buffer", `${stats.BufferMilliseconds || 0} ms`],
      [
        "Packets",
        `${stats.PacketsSent || 0} sent · ${received} received · ${stats.PacketsDropped || 0} dropped`,
      ],
      [
        "Network path",
        [
          stats.ICECandidateType,
          stats.ICEState,
          stats.ICERestartPending ? "reconnecting" : "",
        ]
          .filter(Boolean)
          .join(" · ") || "Direct RTP",
      ],
      [
        "Audio processing",
        `Echo cancellation ${stats.EchoCancellation ? "on" : "off"} · noise suppression ${stats.NoiseSuppression ? "on" : "off"}`,
      ],
    ];
  }
</script>

<div class={["flex flex-col", workspace ? "gap-0" : "gap-6"]}>
  <!-- Without calls, the dialer comes first. -->
  <section class={snapshot.Calls.length === 0 && "order-1"}>
    {#if snapshot.Calls.length || !workspace}<div class="flex flex-wrap items-center justify-between gap-4 wrap-anywhere">
      <h2 class="m-0 text-[1.15rem] font-semibold">{snapshot.Calls.length ? "Current calls" : "Calls"}</h2>
      {#if snapshot.Calls.length}<button
          class={ui.quietButton}
          aria-pressed={showStats}
          onclick={() => (showStats = !showStats)}
          >{showStats ? "Hide call statistics" : "Show call statistics"}</button
        >{/if}
    </div>
    {/if}
    {#if connectedCalls.length >= 2 || hasConference}
      <div class="flex flex-wrap items-center gap-4 mt-4 px-5 py-4 rounded-[8px] border border-brand-border bg-accent-surface">
        <span class="grid place-items-center size-[40px] rounded-full bg-brand text-white"><Icon name="conference" size={20} /></span>
        <div class="flex-1 min-w-[180px]">
          <h3 class={ui.h3}>{hasConference ? "Conference" : `${connectedCalls.length} calls in progress`}</h3>
          <p class={ui.hint}>
            {#if hasConference}Everyone in the conference hears each other.
            {:else if connectedCalls.length > 3}Merge supports up to three calls; hang up the others first.
            {:else}Merge them so everyone can talk together. Calls on hold resume automatically.{/if}
          </p>
        </div>
        {#if hasConference}<button class={ui.button} disabled={busy} onclick={() => run(() => api.LeaveConference())}
            >Split conference</button
          >{:else}<button class={ui.primaryButton}
            disabled={busy || connectedCalls.length > 3}
            onclick={() => run(() => api.Conference(connectedCalls.map((call) => call.ID)))}>Merge into conference</button
          >{/if}
      </div>
    {/if}
    {#each snapshot.Calls as call (call.ID)}
      {@const original = callsByID.get(call.ConsultationFor)}
      {@const consultation = snapshot.Calls.find((c) => c.ConsultationFor === call.ID)}
      {@const warning = blockingMediaWarning(call.Stats)}
      <article class={["min-w-0 rounded-[8px] border", call.State === "ringing" ? "border-brand-border" : "border-border", workspace ? "p-5 mt-4 mb-2 bg-surface-inset" : "p-6 mt-4 bg-surface shadow-panel border-t-panel-edge max-[700px]:p-[1.15rem]"]}>
        <header class="flex items-center gap-4 min-w-0">
          <span class={["grid place-items-center shrink-0 size-[48px] rounded-full border", call.State === "connected" && !call.Held ? "bg-accent-surface text-accent-text border-brand-border" : "bg-control text-muted border-control-border"]}>
            <Icon name={call.Stats?.Conference ? "conference" : call.Direction === "incoming" ? "incoming" : "outgoing"} size={22} />
          </span>
          <div class="flex-1 min-w-0">
            <p class="m-0 text-[0.75rem] text-muted">{call.Account} · {call.Direction === "incoming" ? "Incoming" : "Outgoing"}{call.Stats?.Conference ? " · in conference" : ""}</p>
            <h3 class="m-0 text-[1.2rem] font-semibold truncate" title={call.Remote}>{remoteName(call.Remote)}</h3>
            <p class="m-0 text-[0.78rem] text-subtle truncate">{call.Remote}</p>
          </div>
          <div class="flex flex-col items-end gap-1 shrink-0">
            <span class={[ui.badge, call.Held && "text-[#f5cb7d]", call.State === "connected" && !call.Held && "text-[#7fe0ad]"]}>{stateLabel(call)}</span>
            {#if call.Connected && !call.Connected.startsWith("0001-")}<span class="text-[0.8rem] text-muted tabular-nums">{duration(call.Connected)}</span>{/if}
            {#if call.Muted}<span class="text-[0.75rem] text-[#f3909e]">Muted</span>{/if}
          </div>
        </header>

        {#if original}
          <div class="flex flex-wrap items-center gap-3 mt-4 px-4 py-3 rounded-[6px] border border-brand-border bg-accent-surface">
            <Icon name="transfer" size={18} />
            <span class="flex-1 min-w-[160px] text-[0.9rem]">Transfer of <strong>{remoteName(original.Remote)}</strong> — talk to {remoteName(call.Remote)} first, then connect them.</span>
            <button class={ui.primaryButton} disabled={busy || call.State !== "connected"}
              onclick={() => run(() => api.AttendedTransfer(original.ID, call.ID))}>Complete transfer</button>
            <button class={ui.button} disabled={busy}
              onclick={() => run(async () => {
                await api.Hangup(call.ID);
                if (original.Held) await api.Hold(original.ID, false);
              })}>Cancel and return</button>
          </div>
        {:else if consultation}
          <p class={[ui.hint, "mt-4"]}>Waiting while you talk to {remoteName(consultation.Remote)} about the transfer.</p>
        {/if}
        {#if call.TransferStatus}<p class={[ui.text, "mt-4"]} role="status">Transfer: {call.TransferStatus}</p>{/if}
        {#if call.Stats?.AudioStopped}
          <div class="flex flex-wrap items-center gap-3 mt-4" role="alert">
            <p class={[ui.error, "flex-1"]}>
              Audio device stopped. {call.Stats.AudioRecovering ? "Reconnecting…" : call.Stats.AudioRecoveryError || "Automatic recovery is paused."}
            </p>
            <button class={ui.button} disabled={busy || call.Stats.AudioRecovering || call.Stats.AudioRecoveryPending}
              onclick={() => run(() => api.RetryAudio(call.ID))}>Retry audio</button>
          </div>
        {/if}
        {#if warning}<p class={[ui.error, "mt-4"]} role="status">{warning}</p>{/if}
        {#if call.Error}<p class={[ui.error, "mt-4"]}>{call.Error}</p>{/if}

        <div class="flex flex-wrap items-start gap-2 mt-5">
          {#if call.State === "ringing"}
            <button class={[ui.primaryButton, "inline-flex items-center gap-2"]} disabled={busy}
              onclick={() => run(() => api.Answer(call.ID))}><Icon name="phone" size={18} /> Answer</button>
            <button class={[ui.button, "inline-flex items-center gap-2"]} aria-pressed={panel[call.ID] === "redirect"}
              onclick={() => togglePanel(call.ID, "redirect")}><Icon name="transfer" size={18} /> Redirect</button>
          {/if}
          {#if call.State === "connected"}
            {#snippet control(label, icon, pressed, onclick, disabled = false)}
              <button
                class="
                  flex flex-col items-center gap-[0.35rem] w-[76px] py-2 rounded-[8px] border border-transparent
                  text-[0.75rem] text-muted cursor-pointer outline-offset-2 transition-colors duration-100
                  enabled:hover:bg-control-hover enabled:hover:text-foreground disabled:opacity-40 disabled:cursor-not-allowed
                  aria-pressed:bg-accent-surface aria-pressed:border-brand-border aria-pressed:text-accent-text
                "
                aria-pressed={pressed}
                disabled={busy || disabled}
                {onclick}
              ><span class="grid place-items-center size-[40px] rounded-full bg-control border border-control-border"><Icon name={icon} size={19} /></span>{label}</button>
            {/snippet}
            {@render control(call.Muted ? "Unmute" : "Mute", call.Muted ? "microphone-off" : "microphone", call.Muted, () => run(() => api.Mute(call.ID, !call.Muted)))}
            {@render control(call.Held ? "Resume" : "Hold", "pause", call.Held, () => run(() => api.Hold(call.ID, !call.Held)))}
            {@render control("Keypad", "dialpad", panel[call.ID] === "keypad", () => togglePanel(call.ID, "keypad"), call.Held)}
            {@render control("Transfer", "transfer", panel[call.ID] === "transfer", () => togglePanel(call.ID, "transfer"), Boolean(original))}
            {@render control("Add person", "user-plus", panel[call.ID] === "add", () => togglePanel(call.ID, "add"), connectedCalls.length >= 3)}
            {@render control("More", "settings", panel[call.ID] === "more", () => togglePanel(call.ID, "more"))}
          {/if}
          <button class={[ui.dangerButton, "inline-flex items-center gap-2 ml-auto self-center"]} onclick={() => run(() => api.Hangup(call.ID))}
            ><Icon name="phone" size={18} /> {call.State === "ringing" ? "Decline" : "Hang up"}</button>
        </div>

        {#if panel[call.ID] === "redirect" && call.State === "ringing"}
          <CallTargetPicker
            account={call.Account}
            {snapshot}
            {busy}
            title="Redirect incoming call"
            hint="The caller is sent to this destination without being answered."
            actions={[{ label: "Redirect", primary: true, run: (to) => run(() => api.Redirect(call.ID, to)) }]}
            onclose={() => (panel[call.ID] = "")}
          />
        {:else if panel[call.ID] === "transfer" && call.State === "connected"}
          {@const others = connectedCalls.filter((c) => c.ID !== call.ID && c.Account === call.Account)}
          <CallTargetPicker
            account={call.Account}
            {snapshot}
            {busy}
            title={`Transfer ${remoteName(call.Remote)}`}
            hint="Call first puts this call on hold and rings the recipient, so you can announce the caller before you connect them."
            actions={[
              { label: "Transfer now", run: (to) => run(async () => { await api.Transfer(call.ID, to); panel[call.ID] = ""; }) },
              { label: "Call first", primary: true, run: (to) => run(async () => { await api.Consult(call.ID, to); panel[call.ID] = ""; }) },
            ]}
            onclose={() => (panel[call.ID] = "")}
          />
          {#if others.length}
            <div class="flex flex-wrap items-center gap-2 mt-3">
              <span class={ui.hint}>Or connect with a call in progress:</span>
              {#each others as other (other.ID)}
                <button class={ui.button} disabled={busy} onclick={() => run(() => api.AttendedTransfer(call.ID, other.ID))}>{remoteName(other.Remote)}</button>
              {/each}
            </div>
          {/if}
        {:else if panel[call.ID] === "add" && call.State === "connected"}
          <CallTargetPicker
            account={call.Account}
            {snapshot}
            {busy}
            title="Add a person to this call"
            hint="This call waits on hold while you call. Merge the calls into a conference once they answer."
            actions={[{ label: "Call", primary: true, run: (to) => run(async () => { await api.Dial(call.Account, to); panel[call.ID] = ""; }) }]}
            onclose={() => (panel[call.ID] = "")}
          />
        {:else if panel[call.ID] === "keypad" && call.State === "connected"}
          <div class="mt-5 p-4 rounded-[8px] border border-border bg-surface-inset">
            <div class="grid grid-cols-3 gap-2 max-w-[260px] mx-auto">
              {#each ["1", "2", "3", "4", "5", "6", "7", "8", "9", "*", "0", "#"] as digit}
                <button class={[ui.button, "py-3 text-[1.1rem] tabular-nums"]} disabled={busy || call.Held}
                  onclick={() => run(() => sendTone(call, digit))}>{digit}</button>
              {/each}
            </div>
            <p class={[ui.hint, "mt-3 text-center"]} role="status">{toneNotices[call.ID] || "Digits go to the phone menu of this call."}</p>
          </div>
        {:else if panel[call.ID] === "more" && call.State === "connected"}
          <div class="flex flex-col gap-4 mt-5 p-4 rounded-[8px] border border-border bg-surface-inset">
            <label class={ui.label}
              >Microphone · {Math.round((call.Stats.InputGain ?? 1) * 100)}%<input class={ui.range}
                type="range" min="0" max="2" step="0.1"
                value={call.Stats.InputGain ?? 1}
                onchange={(e) => run(() => api.SetGain(call.ID, Number(e.currentTarget.value), call.Stats.OutputGain ?? 1))}
              /></label>
            <label class={ui.label}
              >Speaker · {Math.round((call.Stats.OutputGain ?? 1) * 100)}%<input class={ui.range}
                type="range" min="0" max="2" step="0.1"
                value={call.Stats.OutputGain ?? 1}
                onchange={(e) => run(() => api.SetGain(call.ID, call.Stats.InputGain ?? 1, Number(e.currentTarget.value)))}
              /></label>
            <div class="flex flex-wrap items-center gap-3">
              <button class={ui.button} disabled={busy}
                onclick={() => run(() => (call.Stats.Recording ? api.StopRecording(call.ID) : api.StartRecording(call.ID)))}
                >{call.Stats.Recording ? "Stop recording" : "Record to WAV…"}</button>
              {#if call.Stats?.ICECandidateType}<button class={ui.button}
                  disabled={busy || restarting[call.ID] || call.Stats.ICERestartPending}
                  onclick={() => restartPath(call.ID)}>Reconnect network audio</button>{/if}
            </div>
            {#if call.Stats.Recording}<p class={ui.hint} role="status">Recording · {call.Stats.RecordingPath}</p>{/if}
            {#if restarting[call.ID] || call.Stats.ICERestartPending}<p class={ui.hint} role="status">Checking a new audio path…</p>{/if}
            {#if restartErrors[call.ID]}<p class={ui.error} role="alert">{restartErrors[call.ID]}</p>{/if}
          </div>
        {/if}

        {#if showStats}<dl class="mt-5 text-xs tabular-nums">
            {#each statistics(call.Stats || {}) as [key, value]}<div class={statRow}>
                <dt class="text-muted">{key}</dt>
                <dd class={statValue}>{value}</dd>
              </div>{/each}
          </dl>
          <details class={[ui.details, ui.section]}>
            <summary class={ui.summary}>All technical statistics</summary>
            <dl class="mt-4 text-xs tabular-nums">
              {#each Object.entries(call.Stats || {}) as [key, value]}<div class={statRow}>
                  <dt class="text-muted">{key}</dt>
                  <dd class={statValue}>{value === null ? "Unavailable" : String(value)}</dd>
                </div>{/each}
            </dl>
          </details>{/if}
      </article>
    {:else}<div class={["mt-4 py-8 text-center rounded-[6px]", workspace ? "text-muted" : "px-6 border border-border bg-surface-secondary"]}>
        <h3 class={workspace ? "m-0 text-[0.9rem] font-normal" : ui.h3}>No active calls</h3>
        {#if !workspace}<p class="m-0 leading-[1.6] text-muted">
          Type a number above to call. Incoming calls will appear here.
        </p>{/if}
      </div>{/each}
  </section>
  <section class={["min-w-0 rounded-[6px]", workspace ? "pt-2 pb-6" : "p-6 border border-border border-t-panel-edge bg-surface shadow-panel max-[700px]:p-[1.15rem]"]}>
    <div class="flex items-center gap-4 mb-7"><span class="grid place-items-center w-[48px] h-[48px] rounded-[10px] bg-accent-surface text-accent border border-brand-border"><Icon name="phone" size={24} /></span><div><h2 class="mx-0 mt-0 mb-1 text-[1.45rem] font-semibold">{workspace ? "Make a call" : "New call"}</h2>{#if workspace}<p class={ui.hint}>Enter a number, SIP address or contact name.</p>{/if}</div></div>
    <form
      class="stack flex flex-col gap-[0.85rem]"
      onsubmit={(event) => {
        event.preventDefault();
        if (!busy && account && target.trim())
          run(() => api.Dial(account, target.trim()));
      }}
    >
      <label class={ui.label} for="call-destination">Name, phone number or SIP address</label>
      <div class="flex items-stretch gap-3 max-[700px]:flex-wrap">
        <input
          id="call-destination"
          class={[ui.input, "dial-input flex-1 p-[0.8rem] text-left text-[1.15rem] max-[700px]:basis-full"]}
          placeholder="Type a number or search contacts"
          autocomplete="off"
          bind:value={target}
        />
        <button class={[ui.primaryButton, "flex shrink-0 items-center justify-center gap-2"]} disabled={busy || !account || !target.trim()}
          ><Icon name="phone" size={18} /> Call</button
        >
      </div>
      <div class={[ui.row, "justify-between flex-wrap"]}>
        <p class={ui.hint}>
          {account
            ? `Calling from ${account}. Press Enter to call.`
            : "Add and select an account to make a call."}
        </p>
        <button
          type="button"
          class={ui.quietButton}
          disabled={busy || !account}
          onclick={() =>
            run(async () => {
              const previous = await api.LastDialed(account);
              if (previous) {
                target = previous;
                redialNotice =
                  "Last number selected. Press Enter or Call to dial.";
              } else
                redialNotice = "No outgoing calls saved for this account yet.";
            })}>Use last number</button
        >
      </div>
      {#if redialNotice}<p class={ui.hint} role="status">
          {redialNotice}
        </p>{/if}
      {#if suggestions.length}
        <div
          class="max-h-[320px] overflow-y-auto pr-1"
          aria-label={target ? "Matching contacts" : "Favorite contacts"}
        >
          <p class={ui.hint}>
            {target ? "Matching contacts" : "Favorites"}
          </p>
          {#each suggestions as contact (contact.ID)}
            <ContactCard
              {contact}
              presence={presenceByTarget.get(extensionOf(contact))}
              onDial={(address) => {
                target = address;
                if (contact.Account) account = contact.Account;
              }}
            />
          {/each}
        </div>
      {/if}
      {#if contactError}<p class={ui.error} role="status">
          Contact search: {contactError}
        </p>{/if}
    </form>
  </section>
  <section class={["order-2", workspace ? "border-t border-border" : "grid grid-cols-[repeat(auto-fit,minmax(min(280px,100%),1fr))] gap-5"]} aria-label="Account services">
    {#if snapshot.Accounts.find((a) => a.Name === account)?.Features?.length}
      <details class={[ui.details, workspace ? "mt-[1.3rem] pt-4" : ui.section]}>
        <summary class={ui.summary}>Phone service shortcuts</summary>
        <div class={[ui.row, "flex-wrap"]}>
          {#each snapshot.Accounts.find((a) => a.Name === account).Features as feature}
            <button class={ui.button}
              disabled={busy ||
                (feature.Action === "transfer" &&
                  !snapshot.Calls.some(
                    (c) =>
                      c.Account === account &&
                      c.State === "connected" &&
                      !c.Held,
                  ))}
              onclick={() =>
                run(() =>
                  api.DialFeature(
                    account,
                    snapshot.Calls.find(
                      (c) =>
                        c.Account === account &&
                        c.State === "connected" &&
                        !c.Held,
                    )?.ID || "",
                    feature.Name,
                    target,
                  ),
                )}>{feature.Name}</button
            >
          {/each}
        </div>
      </details>
    {/if}
    <details class={[ui.details, workspace ? "mt-[1.3rem] pt-4" : ui.section]}>
      <summary class={ui.summary}>Your status & voicemail</summary>
      <div class={[ui.row, "flex-wrap"]}>
        <button class={ui.button}
          disabled={busy || !account}
          onclick={() => run(() => api.WatchVoicemail(account))}
          >Check for voicemail</button
        ><button class={ui.button}
          disabled={busy || !account}
          onclick={() => run(() => api.DialVoicemail(account))}
          >Call voicemail</button
        >
      </div>
      {#each (snapshot.Presence || []).filter((p) => p.Account === account && p.Voicemail) as p}<p class={ui.text}
        >
          {p.Voicemail.Waiting ? "New voicemail" : "No new voicemail"} · {p
            .Voicemail.New} new / {p.Voicemail.Old} old · {p.State}
        </p>{/each}
      {#if currentAccount?.Capabilities?.Status === "unavailable"}
        <p class={ui.hint}>This server does not accept a published status.</p>
      {:else if currentAccount}
        {@const own = statusStyle(snapshot.DND ? "dnd" : currentAccount.PresenceState || "unknown")}
        <div class="flex items-center gap-2 text-[0.9rem]">
          <span class={["size-[10px] rounded-full", own.dot]}></span>
          <span class={own.text}>{currentAccount.PresenceState || snapshot.DND ? own.label : "Not set by Voiper"}</span>
          {#if currentAccount.PresenceNote}<span class="text-muted truncate">· {currentAccount.PresenceNote}</span>{/if}
        </div>
        <label class={ui.label}
          >Status text<input class={ui.field}
            maxlength="512"
            placeholder={currentAccount.PresenceNote || "For example: In a meeting until 14:00"}
            bind:value={statusNote}
          /></label
        >
        <div class={[ui.row, "flex-wrap"]}>
          <button class={ui.button}
            disabled={busy}
            onclick={() => run(() => api.PublishStatus(account, true, statusNote))}
            >Set available</button
          ><button class={ui.button}
            disabled={busy}
            onclick={() => run(() => api.PublishStatus(account, false, statusNote))}
            >Set away</button
          ><button class={ui.quietButton}
            disabled={busy}
            onclick={() => run(() => api.UnpublishStatus(account))}
            >{currentAccount.Capabilities?.Status === "swyx" ? "Reset status" : "Stop publishing"}</button
          >
        </div>
        {#if currentAccount.PresenceError}<p class={[ui.hint, "text-danger"]}>{currentAccount.PresenceError}</p>{/if}
        {#if currentAccount.SwyxAPIError}
          <p class={[ui.hint, "text-danger"]}>{currentAccount.SwyxAPIError}</p>
          {#if currentAccount.SwyxAPIError.includes("certificate")}<button class={ui.button}
              disabled={busy}
              onclick={() => run(() => api.TrustNewSwyxCertificate(account))}
              >Trust the new server certificate</button
            >{/if}
        {/if}
        <p class={ui.hint}>
          {currentAccount.Capabilities?.Status === "swyx"
            ? "Set on the Swyx server, so colleagues see it in SwyxIt! too."
            : "Published with SIP while this account is enabled."}
          Do not disturb also sets your status{snapshot.ScreenLocked ? "; the screen is locked, so you show as away" : ", and you show as away while the screen is locked"}.
        </p>
      {/if}
    </details>
  </section>
</div>
