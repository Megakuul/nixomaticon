<script>
  // Optional settings are grouped in bordered disclosures.
  const optionSummary =
    "list-none mb-[0.8rem] px-[18px] py-[16px] max-[600px]:p-[14px] font-medium text-[#d1dce5] cursor-pointer outline-offset-3";
  const optionBody =
    "stack flex flex-col gap-[18px] px-[18px] pt-[4px] pb-[20px] max-[600px]:px-[14px] max-[600px]:pt-0 max-[600px]:pb-[16px]";
  import { ui } from "../ui.js";
  import { onMount } from "svelte";
  import Dropdown from "../components/Dropdown.svelte";
  import * as api from "../../wailsjs/go/app/App.js";
  let { snapshot, run, busy } = $props();
  let configs = $state({});
  let name = $state("");
  let key = $state("");
  let keys = $state({});
  let editing = $state(false);
  const empty = () => ({
    Server: "",
    ForwardAlways: "",
    ForwardBusy: "",
    ForwardNoAnswer: "",
    NoAnswerSeconds: 20,
    Features: [],
    Codecs: [],
    TLSCAFile: "",
    ICEPolicy: "disabled",
    DelayedOffer: false,
    ICEServers: [],
    MaxRedirects: 0,
    Port: 5060,
    Username: "",
    Password: "",
    UseSecretService: false,
    AutoEnable: false,
    DisplayName: "",
    Domain: "",
    AuthUsername: "",
    Transport: "udp",
    MediaSecurity: "disabled",
    SymmetricRTP: false,
    LocalAddress: "",
    MediaAddress: "",
    OutboundProxy: "",
    PresenceMode: "auto",
    MessagingMode: "auto",
    DTMFMode: "auto",
    Voicemail: "",
    SwyxUsername: "",
    SwyxPassword: "",
  });
  let cfg = $state(empty());
  async function load() {
    configs = await api.ListConfigs();
  }
  onMount(() => {
    run(load);
  });
  async function edit(n) {
    cfg = { ...empty(), ...(await api.GetConfig(n, keys[n] || "")) };
    name = n;
    key = keys[n] || "";
    editing = true;
  }
</script>

<div
  class="
  accounts-layout grid grid-cols-[minmax(230px,0.75fr)_minmax(0,1.5fr)] items-start gap-[24px]
  max-[850px]:grid-cols-[minmax(0,1fr)]
"
>
  <section class={ui.card}>
    <h2 class={ui.h2}>Your accounts</h2>
    <p class="mt-[8px] mb-[24px] mx-0 leading-[1.6] text-muted text-[0.78rem]">
      Enable an account to make and receive calls. You can use more than one at
      a time.
    </p>
    {#each Object.entries(configs) as [n, encrypted]}
      {@const active = snapshot.Accounts.find((a) => a.Name === n)}
      <div
        class="account-item grid gap-[16px] py-[20px] [&+.account-item]:border-t [&+.account-item]:border-border"
      >
        <div class={[ui.row, "justify-between mb-[0.9rem]"]}>
          <strong class="[overflow-wrap:anywhere]">{n}</strong><span
            class={ui.badge}>{active?.State || "disabled"}</span
          >
        </div>
        {#if encrypted}<label class={[ui.label, "mb-[0.9rem]"]}
            >Unlock key<input
              class={ui.field}
              type="password"
              autocomplete="off"
              bind:value={keys[n]}
            /></label
          >{/if}
        <div class={[ui.row, "flex-wrap"]}>
          <button
            class={!active ? ui.primaryButton : ui.button}
            onclick={() =>
              run(async () => {
                if (active) await api.DisableConfig(n);
                else await api.EnableConfig(n, keys[n] || "");
              })}>{active ? "Disable" : "Enable"}</button
          ><button class={ui.button} onclick={() => run(() => edit(n))}
            >Edit</button
          >
          {#if active}<button
              class={ui.button}
              disabled={busy || active.State === "registering"}
              onclick={() => run(() => api.RetryRegistration(n))}
              >Retry registration</button
            >{/if}
          <details class={[ui.details, "w-full"]}>
            <summary class={ui.summary}>Remove account…</summary>
            <div class="stack pt-[12px] flex flex-col gap-4">
              <button
                class={[ui.button, "w-full"]}
                onclick={() => run(() => api.DeleteSavedPassword(n))}
                >Delete saved wallet password</button
              ><button
                class={[ui.dangerButton, "w-full"]}
                onclick={() =>
                  run(async () => {
                    await api.RemoveConfig(n, encrypted);
                    await load();
                  })}>Remove this account</button
              >
            </div>
          </details>
        </div>
        {#if active?.Error}<p class={[ui.error, "wrap-anywhere"]}>
            {active.Error}
          </p>{/if}
        {#if active?.Capabilities}<p class={[ui.hint, "wrap-anywhere"]}>
            {active.Capabilities.Detail}
          </p>{/if}
      </div>
    {:else}<p class={ui.empty}>Add your SIP account to get started.</p>{/each}
  </section>
  <section class={ui.card}>
    <div class={[ui.row, "justify-between flex-wrap"]}>
      <h2 class={ui.h2}>{editing ? "Edit account" : "Add an account"}</h2>
      {#if editing}
        <button
          class={ui.button}
          type="button"
          onclick={() => {
            cfg = empty();
            name = "";
            key = "";
            editing = false;
          }}>Add another account</button
        >
      {/if}
    </div>
    <p class="editor-intro mt-[8px] mb-[24px] mx-0 leading-[1.6] text-muted">
      Your provider supplies the server, extension and password.
    </p>
    <form
      class="stack account-form gap-[20px] flex flex-col"
      onsubmit={(e) => {
        e.preventDefault();
        run(async () => {
          await api.SetConfig(cfg, name, key);
          await load();
        });
      }}
    >
      <div class="form-section grid gap-[20px]">
        <label class={ui.label}
          >Account name<input
            class={ui.field}
            required
            bind:value={name}
            readonly={editing}
          /></label
        >
        <div class={ui.row}>
          <label
            class="grow flex flex-col gap-2 text-[0.9rem] text-label flex-1 min-w-0"
            >SIP server<input
              class={ui.field}
              required
              placeholder="pbx.example.com"
              bind:value={cfg.Server}
            /></label
          ><label
            class="flex flex-col gap-2 text-[0.9rem] text-label max-w-[100px]"
            >Port<input
              class={ui.field}
              type="number"
              min="0"
              max="65535"
              bind:value={cfg.Port}
            /></label
          >
        </div>
        <label class={ui.label}
          >Username / extension<input
            class={ui.field}
            required
            bind:value={cfg.Username}
          /></label
        ><label class={ui.label}
          >SIP password<input
            class={ui.field}
            type="password"
            autocomplete="new-password"
            bind:value={cfg.Password}
          /></label
        ><label class={ui.label}
          >Display name<input
            class={ui.field}
            bind:value={cfg.DisplayName}
          /></label
        >
      </div>
      <div
        class="form-section account-preferences grid gap-[12px] py-[20px] border-y border-border"
      >
        <label class={ui.checkLabel}
          ><input
            class={ui.checkbox}
            type="checkbox"
            bind:checked={cfg.AutoEnable}
          /> Register automatically on startup</label
        >
        <p class={[ui.hint, "pl-[28px]"]}>
          Encrypted account files still require manual unlocking. Desktop wallet
          accounts may ask to unlock the wallet.
        </p>
        <label class={ui.checkLabel}
          ><input
            class={ui.checkbox}
            type="checkbox"
            bind:checked={cfg.UseSecretService}
          /> Store SIP password in the desktop wallet</label
        >
        {#if cfg.UseSecretService}<p class={[ui.hint, "pl-[28px]"]}>
            The account file contains no SIP password. Leave the password blank
            to keep the saved wallet entry; enabling the account may ask to
            unlock your wallet.
          </p>{/if}
      </div>
      <label class={ui.label}
        >Transport<Dropdown
          label="Transport"
          bind:value={cfg.Transport}
          options={[
            { value: "udp", label: "UDP" },
            { value: "tcp", label: "TCP" },
            { value: "tls", label: "TLS · certificate verified" },
          ]}
        /></label
      >
      <div class="form-actions grid justify-items-start gap-[12px]">
        <button class={ui.primaryButton} type="submit">Save account</button>
        <p class={ui.hint}>
          Re-enable this account after saving to apply changes.
        </p>
      </div>
      <h3 class="advanced-heading mt-[12px] mb-0 text-base mx-0 font-semibold">
        Optional settings
      </h3>
      <details
        class={[ui.details, "m-0 p-0 border border-border rounded-[6px]"]}
      >
        <summary class={optionSummary}>SIP connection</summary>
        <div class={optionBody}>
          <label class={ui.checkLabel}
            ><input
              class={ui.checkbox}
              type="checkbox"
              bind:checked={cfg.DelayedOffer}
            /> Let the server offer audio (delayed offer)</label
          >
          <p class={ui.hint}>
            Enable if your provider supplies the audio offer. Reliable
            provisional offers are answered separately for each endpoint;
            microphone capture starts after the final answer.
          </p>
          <label class={ui.label}
            >SIP domain<input
              class={ui.field}
              placeholder="Defaults to server"
              bind:value={cfg.Domain}
            /></label
          ><label class={ui.label}
            >Authentication username<input
              class={ui.field}
              placeholder="Defaults to username"
              bind:value={cfg.AuthUsername}
            /></label
          ><label class={ui.label}
            >Outbound proxy<input
              class={ui.field}
              bind:value={cfg.OutboundProxy}
            /></label
          ><label class={ui.label}
            >Local signaling address<input
              class={ui.field}
              placeholder="Automatic"
              bind:value={cfg.LocalAddress}
            /></label
          ><label class={ui.label}
            >Advertised media IP<input
              class={ui.field}
              placeholder="Determined from route to server"
              bind:value={cfg.MediaAddress}
            /></label
          >
        </div>
      </details>
      <details
        class={[ui.details, "m-0 p-0 border border-border rounded-[6px]"]}
      >
        <summary class={optionSummary}>Call encryption</summary>
        <div class={optionBody}>
          <label class={ui.label}
            >Media encryption<Dropdown
              label="Media encryption"
              bind:value={cfg.MediaSecurity}
              options={[
                { value: "disabled", label: "Plain RTP · compatibility" },
                { value: "optional", label: "Prefer SDES-SRTP · requires TLS" },
                {
                  value: "required",
                  label: "Require SDES-SRTP · requires TLS",
                },
                {
                  value: "dtls",
                  label: "Require DTLS-SRTP · requires TLS and RTCP mux",
                },
              ]}
              onchange={(nextValue) => {
                if (nextValue !== "required") cfg.SymmetricRTP = false;
              }}
            /></label
          >
          <label class={ui.checkLabel}>
            <input
              class={ui.checkbox}
              type="checkbox"
              bind:checked={cfg.SymmetricRTP}
              disabled={cfg.MediaSecurity !== "required"}
            />
            Allow authenticated SRTP port changes
          </label>
          <p class={ui.hint}>
            Requires SDES-SRTP over TLS. Accepts a new port only after packet
            authentication from the same IP address. ICE takes precedence; plain
            RTP and DTLS paths remain fixed.
          </p>
        </div>
      </details>
      <details
        class={[ui.details, "m-0 p-0 border border-border rounded-[6px]"]}
      >
        <summary class={optionSummary}
          >Provider compatibility and voicemail</summary
        >
        <div class={optionBody}>
          <label class={ui.label}
            >DTMF<Dropdown
              label="DTMF"
              bind:value={cfg.DTMFMode}
              options={[
                { value: "auto", label: "Automatic" },
                { value: "rtp", label: "RTP telephone events" },
                { value: "info", label: "SIP INFO · Swyx compatibility" },
              ]}
            /></label
          >
          <label class={ui.label}
            >Presence<Dropdown
              label="Presence"
              bind:value={cfg.PresenceMode}
              options={[
                { value: "auto", label: "Automatic" },
                { value: "standard", label: "Standard SIP" },
                { value: "dialog", label: "Dialog / busy lamp" },
                { value: "swyx", label: "Swyx" },
                { value: "disabled", label: "Disabled" },
              ]}
            /></label
          >
          <label class={ui.label}
            >Messaging<Dropdown
              label="Messaging"
              bind:value={cfg.MessagingMode}
              options={[
                { value: "auto", label: "Automatic" },
                { value: "standard", label: "Standard SIP MESSAGE" },
                { value: "disabled", label: "Disabled" },
              ]}
            /></label
          >
          <label class={ui.label}
            >Voicemail number<input
              class={ui.field}
              bind:value={cfg.Voicemail}
            /></label
          >
        </div>
      </details>
      <details
        class={[ui.details, "m-0 p-0 border border-border rounded-[6px]"]}
      >
        <summary class={optionSummary}
          >Swyx client sign-in (status & chat)</summary
        >
        <div class={optionBody}>
          <p class={ui.hint}>
            SwyxWare does not accept SIP status or chat, so Voiper uses the Swyx
            client API. That login is usually your Windows/AD account (for
            example <code>DOMAIN\user</code> or <code>user@domain</code>), not
            your SIP user name. Leave blank to try the SIP credentials.
          </p>
          <label class={ui.label}
            >Swyx / AD user name<input
              class={ui.field}
              autocomplete="off"
              placeholder="e.g. ananas\user.name"
              bind:value={cfg.SwyxUsername}
            /></label
          >
          <label class={ui.label}
            >Swyx / AD password<input
              class={ui.field}
              type="password"
              autocomplete="new-password"
              bind:value={cfg.SwyxPassword}
            /></label
          >
          {#if cfg.UseSecretService}<p class={[ui.hint, "pl-0"]}>
              The Swyx password is also stored in the desktop wallet. Leave it
              blank to keep the saved one.
            </p>{/if}
        </div>
      </details>
      <details
        class={[ui.details, "m-0 p-0 border border-border rounded-[6px]"]}
      >
        <summary class={optionSummary}>Call forwarding</summary>
        <div class={optionBody}>
          <label class={ui.label}
            >Always forward to<input
              class={ui.field}
              bind:value={cfg.ForwardAlways}
              placeholder="Disabled when empty"
            /></label
          >
          <label class={ui.label}
            >Forward when busy or DND<input
              class={ui.field}
              bind:value={cfg.ForwardBusy}
            /></label
          >
          <label class={ui.label}
            >Forward when unanswered<input
              class={ui.field}
              bind:value={cfg.ForwardNoAnswer}
            /></label
          >
          <label class={ui.label}
            >Ring for seconds<input
              class={ui.field}
              type="number"
              min="5"
              max="300"
              bind:value={cfg.NoAnswerSeconds}
            /></label
          >
          <p class={ui.hint}>
            Forwarding sends a SIP redirect; your PBX must support it. Settings
            apply after re-enabling this account.
          </p>
          <label class={ui.label}
            >Follow remote redirects<Dropdown
              label="Follow remote redirects"
              bind:value={cfg.MaxRedirects}
              options={[
                { value: 0, label: "Do not follow automatically (default)" },
                { value: 3, label: "Follow up to 3 SIP destinations" },
                { value: 5, label: "Follow up to 5 SIP destinations" },
              ]}
            /></label
          >
        </div>
      </details>
      <details
        class={[ui.details, "m-0 p-0 border border-border rounded-[6px]"]}
      >
        <summary class={optionSummary}>Audio codec preference</summary>
        <div class={optionBody}>
          <label class={ui.label}
            >Codec preference<input
              class={ui.field}
              value={(cfg.Codecs || []).join(", ")}
              onchange={(e) =>
                (cfg.Codecs = e.target.value
                  .split(",")
                  .map((v) => v.trim().toUpperCase())
                  .filter(Boolean))}
              placeholder="OPUS, G722, PCMA, PCMU"
            /></label
          >
          <p class={ui.hint}>
            Leave empty for the default quality preference. Both peers negotiate
            a common codec.
          </p>
        </div>
      </details>
      <details
        class={[ui.details, "m-0 p-0 border border-border rounded-[6px]"]}
      >
        <summary class={optionSummary}
          >Network traversal and certificates</summary
        >
        <div class={optionBody}>
          <label class={ui.label}
            >Additional TLS CA file<input
              class={ui.field}
              bind:value={cfg.TLSCAFile}
              placeholder="System trust by default"
            /></label
          >
          <label class={ui.label}
            >ICE traversal<Dropdown
              label="ICE traversal"
              bind:value={cfg.ICEPolicy}
              options={[
                { value: "disabled", label: "Disabled · PBX compatibility" },
                {
                  value: "auto",
                  label: "Offer ICE; allow ordinary RTP when peer omits it",
                },
                {
                  value: "required",
                  label: "Require ICE and RTCP multiplexing",
                },
              ]}
            /></label
          >
          {#each cfg.ICEServers || [] as server, i}
            <div
              class="stack repeated-fields p-[18px] border border-border rounded-[6px] flex flex-col gap-4"
            >
              <label class={ui.label}
                >STUN / TURN URLs<input
                  class={ui.field}
                  value={(server.URLs || []).join(", ")}
                  onchange={(e) =>
                    (server.URLs = e.target.value
                      .split(",")
                      .map((v) => v.trim())
                      .filter(Boolean))}
                  placeholder="stun:host:3478, turn:host:3478"
                /></label
              >
              <label class={ui.label}
                >TURN username<input
                  class={ui.field}
                  bind:value={server.Username}
                /></label
              >
              <label class={ui.label}
                >TURN credential<input
                  class={ui.field}
                  type="password"
                  autocomplete="new-password"
                  bind:value={server.Credential}
                /></label
              >
              <button
                class={[ui.button, "self-start"]}
                type="button"
                onclick={() =>
                  (cfg.ICEServers = cfg.ICEServers.filter((_, n) => n !== i))}
                >Remove server</button
              >
            </div>
          {/each}
          <button
            class={ui.button}
            type="button"
            disabled={(cfg.ICEServers || []).length >= 8}
            onclick={() =>
              (cfg.ICEServers = [
                ...(cfg.ICEServers || []),
                { URLs: [], Username: "", Credential: "" },
              ])}>Add STUN / TURN server</button
          >
          <p class={ui.hint}>
            TURN credentials are saved in the account file; use an encryption
            key to encrypt the whole profile.
          </p>
        </div>
      </details>
      <details
        class={[ui.details, "m-0 p-0 border border-border rounded-[6px]"]}
      >
        <summary class={optionSummary}>PBX feature codes</summary>
        <div class={optionBody}>
          <p class={ui.hint}>
            Configure your PBX's pickup, parking, unpark or conference codes.
            Use {"{number}"} in a target to insert the dial field.
          </p>
          {#each cfg.Features || [] as feature, i}
            <div
              class="stack repeated-fields p-[18px] border border-border rounded-[6px] flex flex-col gap-4"
            >
              <label class={ui.label}
                >Feature name<input
                  class={ui.field}
                  bind:value={feature.Name}
                  placeholder="Call pickup"
                /></label
              >
              <label class={ui.label}
                >Target<input
                  class={ui.field}
                  bind:value={feature.Target}
                  placeholder="*8"
                /></label
              >
              <label class={ui.label}
                >Action<Dropdown
                  label="Feature action"
                  bind:value={feature.Action}
                  options={[
                    { value: "dial", label: "Dial" },
                    { value: "transfer", label: "Transfer an active call" },
                  ]}
                /></label
              >
              <button
                class={[ui.button, "self-start"]}
                type="button"
                onclick={() =>
                  (cfg.Features = cfg.Features.filter((_, n) => n !== i))}
                >Remove feature</button
              >
            </div>
          {/each}
          <button
            class={ui.button}
            type="button"
            disabled={(cfg.Features || []).length >= 20}
            onclick={() =>
              (cfg.Features = [
                ...(cfg.Features || []),
                { Name: "", Target: "", Action: "dial" },
              ])}>Add feature code</button
          >
        </div>
      </details>
      <details
        class={[ui.details, "m-0 p-0 border border-border rounded-[6px]"]}
      >
        <summary class={optionSummary}>Account file encryption</summary>
        <div class={optionBody}>
          <label class={ui.label}
            >Configuration encryption key<input
              class={ui.field}
              type="password"
              autocomplete="new-password"
              placeholder="Empty stores the account without encryption"
              bind:value={key}
            /></label
          >
          <p class={ui.hint}>
            Keep the same encryption mode when editing an existing account.
          </p>
        </div>
      </details>
      <div class="form-actions grid justify-items-start gap-[12px]">
        <button class={ui.primaryButton} type="submit">Save account</button>
      </div>
    </form>
  </section>
</div>
