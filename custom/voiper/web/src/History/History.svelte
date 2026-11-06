<script>
  import { ui } from "../ui.js";
  import { onMount } from "svelte";
  import * as api from "../../wailsjs/go/app/App.js";
  let { account, run, busy, onDial, onSave } = $props();
  let history = $state([]),
    search = $state(""),
    allAccounts = $state(true),
    hasMore = $state(false),
    confirmClear = $state(false);
  async function load(more = false) {
    const before = more && history.length ? history.at(-1).ID : 0;
    const rows = await api.HistoryPage(
      allAccounts ? "" : account,
      search,
      before,
      100,
    );
    history = more ? [...history, ...rows] : rows;
    hasMore = rows.length === 100;
  }
  onMount(() => {
    run(() => load());
  });
</script>

<section class={ui.panel}>
  <div class={[ui.row, "justify-between"]}>
    <h2 class={ui.h2}>Recent calls</h2>
    <button class={ui.button} onclick={() => (confirmClear = !confirmClear)}>Clear history</button
    >
  </div>
  {#if confirmClear}<div class="flex flex-wrap items-center gap-2 mb-4 px-4 py-3 rounded-[6px] border border-brand-border bg-accent-surface">
      <span class="mr-auto">Remove all call history on this device?</span><button
        class={ui.dangerButton}
        onclick={() =>
          run(async () => {
            await api.ClearHistory();
            confirmClear = false;
            await load();
          })}>Clear all</button
      ><button class={ui.button} onclick={() => (confirmClear = false)}>Cancel</button>
    </div>{/if}
  <form
    class={[ui.row, "flex-wrap"]}
    onsubmit={(e) => {
      e.preventDefault();
      run(() => load());
    }}
  >
    <input class={ui.field}
      aria-label="Search history"
      placeholder="Number or SIP address"
      bind:value={search}
    /><label class={ui.checkLabel}
      ><input class={ui.checkbox} type="checkbox" bind:checked={allAccounts} /> All accounts</label
    ><button class={ui.button}>Search</button>
  </form>
  <div class="mt-2">
    {#each history as h (h.ID)}<div class="flex flex-wrap items-center gap-[0.7rem] py-4 border-b border-border [&_button]:text-[0.8rem]">
        <div class="flex-1 min-w-0 wrap-anywhere">
          <strong class="block mb-[0.3rem]">{h.Remote}</strong><span class="block text-muted"
            >{h.Direction} · {h.Status} · {h.Account}</span
          ><span class="block text-muted text-[0.78rem]"
            >{new Date(h.Started).toLocaleString()} · {Math.max(
              0,
              Math.round((new Date(h.Ended) - new Date(h.Started)) / 1000),
            )} seconds</span
          >
        </div>
        <button class={ui.button} onclick={() => onDial(h)}>Call again</button><button class={ui.button}
          onclick={() => onSave(h)}>Save contact</button
        ><button
          class={ui.quietButton}
          disabled={busy}
          onclick={() =>
            run(async () => {
              await api.DeleteHistory(h.ID);
              history = history.filter((c) => c.ID !== h.ID);
            })}>Delete</button
        >
      </div>{:else}<p class={ui.empty}>No calls match this view.</p>{/each}
  </div>
  {#if hasMore}<button class={ui.button} disabled={busy} onclick={() => run(() => load(true))}
      >Load earlier calls</button
    >{/if}
</section>
