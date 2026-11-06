<script>
  import { ui } from "./ui.js";
  import Phone from "./Phone/Phone.svelte";
  import Icon from "./components/Icon.svelte";
  import * as api from "../wailsjs/go/app/App.js";

  let { snapshot, account = $bindable(""), target = $bindable(""), run, busy, manageContacts } = $props();
  let recent = $state([]);
  let loading = $state(false);
  let error = $state("");
  const callIDs = $derived(snapshot.Calls.map((call) => call.ID).join(","));

  $effect(() => {
    // Reload when a call starts/ends, not for every live media statistics update.
    callIDs;
    let cancelled = false;
    loading = true;
    api.HistoryPage("", "", 0, 50).then((rows) => {
      if (cancelled) return;
      const seen = new Set();
      recent = rows.filter((row) => {
        const key = `${row.Account}:${row.Remote}`;
        if (seen.has(key)) return false;
        seen.add(key);
        return true;
      }).slice(0, 8);
      error = "";
    }).catch((e) => { if (!cancelled) error = String(e); })
      .finally(() => { if (!cancelled) loading = false; });
    return () => { cancelled = true; };
  });
</script>

<div class="
  grid grid-cols-[minmax(0,_1fr)_290px] rounded-[9px] bg-surface [box-shadow:0_4px_12px_#0003] border
  border-[#4c5b69] *:min-w-0 max-[1100px]:grid-cols-[minmax(0,_1fr)_245px]
  max-[850px]:grid-cols-[minmax(0,_1fr)]
">
  <div class="min-w-0 p-8 max-[1100px]:p-6 max-[850px]:p-5">
    <Phone {snapshot} bind:account bind:target {run} {busy} workspace />
  </div>
  <aside class="
    px-[1.1rem] py-7 min-w-0 border-l border-l-border bg-surface-secondary rounded-[0_9px_9px_0]
    max-[850px]:border-l-0 max-[850px]:border-t max-[850px]:border-border
    max-[850px]:rounded-[0_0_9px_9px]
  " aria-label="Recently called people">
    <div class="flex items-center gap-[0.6rem] mb-[0.4rem]"><Icon name="history" /><h2 class="m-0 text-base font-semibold">Recent people</h2></div>
    <p class={ui.hint}>Pick up where you left off.</p>
    {#if error}<p class={ui.error} role="alert">{error}</p>{/if}
    <div class="mx-0 my-5" aria-busy={loading}>
      {#each recent as person (person.ID)}
        <button class={[ui.control, `
          flex items-center gap-[0.65rem] w-full min-h-[38px] px-[0.3rem] py-[0.85rem]
          rounded-[6px] border-b border-border bg-transparent text-left font-medium
          enabled:hover:bg-control-hover enabled:hover:border-control-border-hover
        `]} title={`Select ${person.Remote} to call`}
          onclick={() => { target = person.Remote; account = person.Account; }}>
          <span class="grid place-items-center w-[32px] h-[32px] shrink-0 rounded-[6px] bg-accent-surface text-accent"><Icon name={person.Direction === "incoming" ? "incoming" : "outgoing"} size={18} /></span>
          <span class="flex-1 min-w-0"><strong class="block text-[0.85rem] wrap-anywhere">{person.Remote}</strong><span class="block mt-[0.2rem] text-[0.7rem] text-muted">{person.Account} · {new Date(person.Started).toLocaleDateString()}</span></span>
          <Icon name="phone" size={16} />
        </button>
      {:else}<div class="px-2 py-8 grid justify-items-center gap-4 text-muted text-center text-[0.85rem]"><Icon name="history" size={28} /><p class={ui.text}>{loading ? "Loading recent calls…" : "Your recent calls will appear here."}</p></div>{/each}
    </div>
    <button class={[ui.control, `
      px-4 py-[0.65rem] rounded-[6px] bg-transparent text-inherit min-h-[38px] shadow-none font-medium flex
      w-full items-center justify-center gap-[0.6rem] border border-control-border
      enabled:hover:bg-control-hover enabled:hover:border-control-border-hover
    `]} onclick={manageContacts}><Icon name="contacts" size={18} /> All contacts <Icon name="arrow" size={16} /></button>
  </aside>
</div>
