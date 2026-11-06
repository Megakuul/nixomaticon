<script>
  // Finds a destination for an active call: a typed number or a contact.
  // Choosing a contact or one of its numbers fills the destination; the
  // actions then act on it (for example "Call first" and "Transfer now").
  import { ui } from "../ui.js";
  import Icon from "../components/Icon.svelte";
  import ContactCard from "../Contacts/ContactCard.svelte";
  import { extensionOf } from "../Contacts/presence.js";
  import * as api from "../../wailsjs/go/app/App.js";

  let { account, snapshot, busy, title, hint = "", actions, onclose } = $props();
  let query = $state("");
  let destination = $state("");
  let destinationName = $state("");
  let contacts = $state([]);
  let searchError = $state("");
  let input;

  $effect(() => {
    input?.focus();
  });
  $effect(() => {
    const text = query.trim();
    let cancelled = false;
    const timer = setTimeout(async () => {
      try {
        const page = await api.ContactsPage(text, 0, 6);
        if (!cancelled) {
          contacts = text ? page : page.filter((c) => c.Favorite);
          searchError = "";
        }
      } catch (error) {
        if (!cancelled) searchError = String(error);
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
  $effect(() => {
    const extensions = contacts.map(extensionOf).filter(Boolean);
    if (account && extensions.length) api.WatchVisiblePresence(account, extensions).catch(() => {});
  });

  // A typed number is the destination until a contact is chosen.
  let target = $derived(destination || query.trim());
</script>

<div
  class="mt-5 rounded-[8px] border border-border bg-surface-inset p-4"
  role="dialog"
  aria-label={title}
  tabindex="-1"
  onkeydown={(e) => e.key === "Escape" && onclose()}
>
  <div class="flex items-center justify-between gap-3 mb-3">
    <h3 class={ui.h3}>{title}</h3>
    <button type="button" class={ui.iconButton} aria-label="Close" onclick={onclose}><Icon name="close" /></button>
  </div>
  <label class="relative block">
    <span class="sr-only">Name, number or SIP address</span>
    <span class="absolute left-3 top-1/2 -translate-y-1/2 text-muted pointer-events-none"><Icon name="search" size={16} /></span>
    <input
      bind:this={input}
      class={[ui.input, "pl-9 pr-3 py-[0.6rem]"]}
      placeholder="Search contacts or type a number"
      autocomplete="off"
      bind:value={query}
      oninput={() => {
        destination = "";
        destinationName = "";
      }}
    />
  </label>
  {#if hint}<p class={[ui.hint, "mt-2"]}>{hint}</p>{/if}
  {#if contacts.length}
    <div class="mt-2 max-h-[260px] overflow-y-auto pr-1" aria-label="Matching contacts">
      {#each contacts as contact (contact.ID)}
        <ContactCard
          {contact}
          presence={presenceByTarget.get(extensionOf(contact))}
          onDial={(address) => {
            destination = address;
            destinationName = contact.Name;
          }}
        />
      {/each}
    </div>
  {:else if query.trim()}
    <p class={[ui.hint, "mt-3"]}>No contact matches. The typed number is used as is.</p>
  {/if}
  {#if searchError}<p class={[ui.error, "mt-2"]}>{searchError}</p>{/if}
  <div class="flex flex-wrap items-center gap-3 mt-4 pt-4 border-t border-border">
    <span class="mr-auto min-w-0 text-[0.9rem] wrap-anywhere">
      {#if target}
        <span class="text-muted">To</span>
        <strong class="font-semibold">{destinationName || target}</strong>
        {#if destinationName}<span class="text-muted tabular-nums">· {target}</span>{/if}
      {:else}<span class="text-muted">Choose a contact or type a number.</span>{/if}
    </span>
    {#each actions as action}
      <button
        type="button"
        class={action.primary ? ui.primaryButton : ui.button}
        disabled={busy || !target}
        onclick={() => action.run(target)}>{action.label}</button
      >
    {/each}
  </div>
</div>
