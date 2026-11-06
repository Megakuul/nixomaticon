<script>
  import Icon from "../components/Icon.svelte";
  import { contactNumbers, extensionOf, initials, statusStyle } from "./presence.js";

  // presence is the watched status of the contact's extension, if any.
  // Message, edit and delete actions appear only when their callbacks are given.
  let { contact, presence, onDial, onMessage, onEdit, onDelete, busy = false } = $props();

  let numbers = $derived(contactNumbers(contact));
  let extension = $derived(extensionOf(contact));
  let status = $derived(presence ? statusStyle(presence.State) : null);
  let note = $derived(presence?.State === "unavailable" ? "" : (presence?.Note || "").trim());
  let preferred = $derived(extension || contact.Address);
  let mobile = $derived(numbers.some((n) => n.kind === "mobile"));

  const chip = `
    inline-flex items-center gap-[0.4rem] px-[0.6rem] py-[0.3rem] rounded-full border text-[0.78rem]
    leading-none cursor-pointer outline-offset-2 transition-colors duration-100 tabular-nums
  `;
  const iconButton = `
    grid place-items-center size-[32px] rounded-[6px] text-muted border border-transparent cursor-pointer
    outline-offset-2 transition-colors duration-100 hover:bg-[#3a4652] hover:text-foreground
    disabled:opacity-40 disabled:cursor-not-allowed
  `;
</script>

<article class="py-3 border-b border-border/70 last:border-b-0">
  <div class="flex items-center gap-2">
    <button
      type="button"
      class="
        flex flex-1 min-w-0 items-center gap-3 p-[0.35rem] -ml-[0.35rem] rounded-[8px] text-left cursor-pointer
        outline-offset-2 transition-colors duration-100 hover:bg-[#323d48]
      "
      title={`Call ${contact.Name || preferred} on ${preferred}`}
      onclick={() => onDial(preferred)}
    >
      <span
        class={[
          "relative grid place-items-center shrink-0 size-[40px] rounded-full text-[0.82rem] font-semibold tracking-wide",
          extension
            ? "bg-accent-surface text-accent-text border border-brand-border"
            : "bg-[#2c3640] text-muted border border-[#465564]",
        ]}
        aria-hidden="true"
      >
        {#if extension}{initials(contact.Name)}{:else}<Icon name={mobile ? "mobile" : "phone"} size={18} />{/if}
        {#if status}<span class={["absolute -right-px -bottom-px size-[12px] rounded-full ring-[2.5px] ring-surface", status.dot]}></span>{/if}
      </span>
      <span class="flex flex-col gap-[0.2rem] min-w-0">
        <span class="flex items-center gap-[0.4rem] min-w-0">
          <strong class="font-semibold text-[0.95rem] text-foreground truncate">{contact.Name || preferred}</strong>
          {#if contact.Company}<span class="shrink-0 text-[0.75rem] text-subtle">{contact.Company}</span>{/if}
          {#if contact.Favorite}<span class="text-[#f2c45a] shrink-0" title="Favorite"><Icon name="star" size={14} /></span>{/if}
          {#if contact.Source === "directory-sync"}<span class="shrink-0 px-[0.4rem] py-px rounded-[4px] bg-[#35414c] text-[0.65rem] text-muted uppercase tracking-wider">Directory</span>{/if}
        </span>
        {#if extension}
          <span class="flex items-center gap-[0.35rem] min-w-0 text-[0.8rem]" title={presence?.Updated ? `Updated ${new Date(presence.Updated).toLocaleString()}` : undefined}>
            {#if status}
              <span class={["shrink-0 font-medium", status.text]}>{status.label}</span>
              {#if note}<span class="text-subtle truncate" title={note}>· {note}</span>{/if}
            {:else}
              <span class="text-subtle">Internal · status not loaded</span>
            {/if}
          </span>
        {:else}
          <span class="text-[0.8rem] text-subtle">{mobile ? "Mobile contact" : "External contact"}</span>
        {/if}
      </span>
    </button>
    {#if onMessage || onEdit || onDelete}
      <div class="flex items-center gap-[0.15rem] shrink-0">
        {#if onMessage}<button type="button" class={iconButton} aria-label={`Message ${contact.Name}`} title="Message" onclick={onMessage}><Icon name="messages" size={17} /></button>{/if}
        {#if onEdit}<button type="button" class={iconButton} aria-label={`Edit ${contact.Name}`} title="Edit" onclick={onEdit}><Icon name="edit" size={17} /></button>{/if}
        {#if onDelete}<button type="button" class={[iconButton, "hover:text-[#ffc2cc] hover:bg-[#382027]"]} aria-label={`Delete ${contact.Name}`} title="Delete" disabled={busy} onclick={onDelete}><Icon name="trash" size={17} /></button>{/if}
      </div>
    {/if}
  </div>
  <div class="flex flex-wrap gap-[0.4rem] mt-2 pl-[52px]">
    {#each numbers as number (number.Address)}
      <button
        type="button"
        class={[
          chip,
          number.kind === "internal"
            ? "bg-accent-surface border-brand-border text-accent-text hover:bg-accent-hover"
            : "bg-[#25303a] border-[#465564] text-[#d5dde5] hover:bg-[#33404c]",
        ]}
        title={`Call ${number.Address}${number.Label ? ` (${number.Label})` : ""}`}
        onclick={() => onDial(number.Address)}
      >
        <Icon name={number.kind === "internal" ? "building" : number.kind === "mobile" ? "mobile" : "phone"} size={13} />
        <span class="text-[#9aa6b2]">{number.kind === "internal" ? "Ext." : number.kind === "mobile" ? "Mobile" : number.Label || "Phone"}</span>
        <span>{number.Address}</span>
      </button>
    {/each}
  </div>
</article>
