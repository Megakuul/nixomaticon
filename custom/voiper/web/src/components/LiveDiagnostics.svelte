<script>
  import { ui } from "../ui.js";
  import * as api from "../../wailsjs/go/app/App.js";
  let { snapshot, logs, run, busy } = $props();
</script>

<section class={[ui.panel, "live-diagnostics"]} aria-label="Live diagnostics">
  <div class={[ui.row, "justify-between flex-wrap"]}>
    <h2 class={ui.h2}>Live diagnostics</h2>
    <span class="text-muted text-[0.78rem]">Updates automatically</span>
  </div>
  <div class="diagnostic-accounts">
    {#each snapshot.Accounts as account (account.Name)}
      <div class="py-4 border-b border-border">
        <div class={[ui.row, "flex-wrap"]}>
          <strong>{account.Name}</strong>
          <span class={[
            "px-[0.7rem] py-[0.3rem] rounded-[4px] text-xs",
            account.State === "registered" ? "text-[#a7d7b9] bg-[#253b30]" :
            account.State === "failed" ? "text-[#ffc2cc] bg-[#382027]" : "bg-control-hover",
          ]}>{account.State}</span>
          <button class={[ui.button, "ml-auto text-[0.85rem]"]} disabled={busy || account.State === "registering"}
            onclick={() => run(() => api.RetryRegistration(account.Name))}>Retry registration</button>
        </div>
        {#if account.Error}<p class={[ui.error, "mt-2"]}>{account.Error}</p>{/if}
        {#if account.PresenceError}<p class={[ui.error, "mt-2"]}>Presence: {account.PresenceError}</p>{/if}
        {#if account.SwyxAPI}<p class={[ui.hint, "mt-2"]}>Swyx client API: {account.SwyxAPI}</p>{/if}
        {#if account.SwyxAPIError}<p class={[ui.error, "mt-2"]}>Swyx client API: {account.SwyxAPIError}</p>{/if}
        {#if account.Capabilities?.Detail}<p class={[ui.hint, "mt-2"]}>{account.Capabilities.Detail}</p>{/if}
      </div>
    {:else}<p class="m-0 leading-[1.6] text-muted">No enabled accounts. Enable an account in Accounts to connect.</p>{/each}
  </div>
  {#each snapshot.Calls as call (call.ID)}
    <div class="px-0 py-4 border-b border-border">
      <div class={[ui.row, "justify-between flex-wrap"]}><strong>{call.Remote}</strong><span>{call.State}</span></div>
      <dl class="grid grid-cols-[repeat(auto-fit,_minmax(120px,_1fr))] gap-4 mt-4 mb-0 mx-0 tabular-nums">
        <div><dt class="text-[0.8rem] text-muted">Codec</dt><dd class="mx-0 mt-1 mb-0">{call.Stats?.Codec || "Negotiating"}</dd></div>
        <div><dt class="text-[0.8rem] text-muted">Jitter</dt><dd class="mx-0 mt-1 mb-0">{(call.Stats?.JitterMilliseconds || 0).toFixed(1)} ms</dd></div>
        <div><dt class="text-[0.8rem] text-muted">Packets lost</dt><dd class="mx-0 mt-1 mb-0">{call.Stats?.PacketsLost || 0}</dd></div>
        <div><dt class="text-[0.8rem] text-muted">Round trip</dt><dd class="mx-0 mt-1 mb-0">{call.Stats?.RTTMilliseconds > 0 ? `${call.Stats.RTTMilliseconds.toFixed(1)} ms` : "Waiting"}</dd></div>
      </dl>
      {#if call.Error}<p class={ui.error}>{call.Error}</p>{/if}
      <!-- Media errors and warnings live here; the call view only shows problems that stop audio. -->
      {#if call.Stats?.LastError}<p class={[ui.error, "mt-2"]}>Media: {call.Stats.LastError}</p>{/if}
      {#if call.Stats?.MediaWarning}<p class={[ui.hint, "mt-2"]}>{call.Stats.MediaWarning}</p>{/if}
    </div>
  {/each}
  <details class={[ui.details, ui.section]}>
    <summary class={ui.summary}>Recent application events ({Math.min(logs.length, 20)})</summary>
    <pre class="p-4 whitespace-pre-wrap wrap-anywhere max-h-[60vh] overflow-auto text-xs bg-field rounded-[6px]">{logs.slice(-20).join("\n") || "No log entries yet."}</pre>
  </details>
</section>
