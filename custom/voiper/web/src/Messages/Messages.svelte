<script>
  import { ui } from "../ui.js";
  import { onMount, tick } from "svelte";
  import Icon from "../components/Icon.svelte";
  import { extensionOf, initials, isExtension, statusStyle } from "../Contacts/presence.js";
  import * as api from "../../wailsjs/go/app/App.js";
  import { EventsOn } from "../../wailsjs/runtime/runtime.js";
  let { account, snapshot, target = $bindable(""), run, busy, onDial, onSave } = $props();
  let conversations = $state([]);
  let messages = $state([]);
  let draft = $state("");
  let search = $state("");
  let messageSearch = $state("");
  let showMessageSearch = $state(false);
  let selected = $state("");
  let composing = $state(false);
  let loadingOlder = $state(false);
  let hasOlder = $state(false);
  let confirmDelete = $state(false);
  // Contact names by the user part of a conversation address.
  let names = $state({});
  let thread = $state();
  let composer = $state();
  let refreshing = false;
  let disposed = false;
  let lastAccount = "";
  let viewRevision = 0;

  let currentAccount = $derived((snapshot?.Accounts || []).find((a) => a.Name === account));
  let messaging = $derived(currentAccount?.Capabilities?.Messaging || "sip");
  let presenceByUser = $derived(
    new Map(
      (snapshot?.Presence || [])
        .filter((p) => p.Account === account && !p.Voicemail)
        .map((p) => [userOf(p.Target), p]),
    ),
  );

  // "sip:318@pbx" → "318"
  function userOf(remote) {
    return (remote || "").replace(/^sips?:/, "").replace(/@.*$/, "");
  }
  function displayName(remote) {
    return names[userOf(remote)] || userOf(remote) || remote;
  }
  function sameDay(a, b) {
    return new Date(a).toDateString() === new Date(b).toDateString();
  }
  function dayLabel(date) {
    const day = new Date(date);
    const today = new Date();
    const yesterday = new Date(Date.now() - 864e5);
    if (sameDay(day, today)) return "Today";
    if (sameDay(day, yesterday)) return "Yesterday";
    return day.toLocaleDateString(undefined, { weekday: "long", day: "numeric", month: "long", year: day.getFullYear() === today.getFullYear() ? undefined : "numeric" });
  }
  function timeLabel(date) {
    return new Date(date).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
  }
  function listTime(date) {
    return sameDay(date, Date.now()) ? timeLabel(date) : new Date(date).toLocaleDateString(undefined, { day: "numeric", month: "short" });
  }
  // Messages in the same direction within five minutes form one group: they
  // sit closer together and only the last one shows the time.
  function continues(previous, message) {
    return previous && previous.Direction === message.Direction && new Date(message.Created) - new Date(previous.Created) < 5 * 60e3;
  }

  async function resolveNames(list) {
    for (const c of list.slice(0, 60)) {
      const user = userOf(c.Remote);
      if (!user || user in names) continue;
      names[user] = "";
      try {
        const found = await api.ContactsPage(user, 0, 5);
        const match = found.find((contact) => extensionOf(contact) === user || contact.Address === user || (contact.Numbers || []).some((n) => n.Address === user));
        if (match?.Name) names[user] = match.Name;
      } catch {
        // Names are a convenience; the address is shown instead.
      }
    }
  }
  async function refresh() {
    if (refreshing || disposed || !account) return;
    refreshing = true;
    const currentAccount = account;
    const currentRemote = selected;
    const revision = viewRevision;
    try {
      const nextConversations = await api.Conversations(currentAccount, search);
      if (account !== currentAccount || revision !== viewRevision || disposed) return;
      if (JSON.stringify(nextConversations) !== JSON.stringify(conversations)) {
        conversations = nextConversations;
        resolveNames(nextConversations);
      }
      if (currentRemote) {
        const latest = await api.MessagesPage(currentAccount, currentRemote, messageSearch, 0, 100);
        if (account !== currentAccount || selected !== currentRemote || revision !== viewRevision || disposed) return;
        const nearBottom = !thread || thread.scrollHeight - thread.scrollTop - thread.clientHeight < 80;
        const latestIDs = new Set(latest.map((m) => m.ID));
        const nextMessages = [
          ...messages.filter((m) => !latestIDs.has(m.ID) && (!latest.length || m.ID < latest[0].ID)),
          ...latest,
        ];
        const messagesChanged = JSON.stringify(nextMessages) !== JSON.stringify(messages);
        if (messagesChanged) messages = nextMessages;
        if (!loadingOlder) hasOlder = latest.length === 100;
        if (latest.length && nearBottom && !messageSearch && document.hasFocus())
          await api.MarkConversationRead(currentAccount, currentRemote, latest.at(-1).ID);
        if (nearBottom && messagesChanged) {
          await tick();
          thread?.scrollTo({ top: thread.scrollHeight });
        }
      }
    } finally {
      refreshing = false;
    }
  }
  async function openConversation(remote) {
    viewRevision++;
    selected = remote;
    target = remote;
    composing = false;
    messages = [];
    messageSearch = "";
    showMessageSearch = false;
    loadingOlder = false;
    confirmDelete = false;
    resolveNames([{ Remote: remote }]);
    await refresh();
    await tick();
    composer?.focus();
  }
  async function older() {
    if (!messages.length) return;
    const currentAccount = account,
      currentRemote = selected;
    const revision = viewRevision;
    const previousHeight = thread?.scrollHeight || 0;
    const rows = await api.MessagesPage(currentAccount, currentRemote, messageSearch, messages[0].ID, 100);
    if (account !== currentAccount || selected !== currentRemote || revision !== viewRevision || disposed) return;
    messages = [...rows, ...messages];
    loadingOlder = true;
    hasOlder = rows.length === 100;
    // Keep the message that was at the top in place.
    await tick();
    if (thread) thread.scrollTop += thread.scrollHeight - previousHeight;
  }
  async function send() {
    const sendingAccount = account;
    const recipient = selected || target;
    const submitted = draft;
    const revision = viewRevision;
    if (!recipient || !submitted.trim()) return;
    await api.SendMessage(sendingAccount, recipient, submitted);
    if (disposed || account !== sendingAccount || revision !== viewRevision) return;
    if (draft === submitted) draft = "";
    selected = recipient;
    await refresh();
    await tick();
    thread?.scrollTo({ top: thread.scrollHeight });
  }
  $effect(() => {
    if (account !== lastAccount) {
      viewRevision++;
      lastAccount = account;
      selected = "";
      messages = [];
      conversations = [];
      run(refresh);
    }
  });
  // The composer grows with its text up to a few lines.
  $effect(() => {
    draft;
    if (!composer) return;
    composer.style.height = "auto";
    composer.style.height = `${Math.min(composer.scrollHeight, 160)}px`;
  });
  onMount(() => {
    disposed = false;
    if (target) selected = target;
    run(refresh);
    const off = EventsOn("incoming-message", () => run(refresh));
    const timer = setInterval(() => {
      refresh().catch(() => {});
    }, 2000);
    return () => {
      disposed = true;
      off();
      clearInterval(timer);
    };
  });

  const headerButton = `
    grid place-items-center size-[36px] rounded-full text-muted cursor-pointer outline-offset-2
    transition-colors duration-100 enabled:hover:bg-control-hover enabled:hover:text-foreground
    disabled:opacity-40 disabled:cursor-not-allowed aria-pressed:bg-accent-surface aria-pressed:text-accent-text
  `;
</script>

{#snippet avatar(remote, size = 40)}
  {@const user = userOf(remote)}
  {@const presence = presenceByUser.get(user)}
  {@const status = presence ? statusStyle(presence.State) : null}
  <span
    class={[
      "relative grid place-items-center shrink-0 rounded-full font-semibold tracking-wide",
      isExtension(user) ? "bg-accent-surface text-accent-text border border-brand-border" : "bg-control text-muted border border-control-border",
    ]}
    style={`width:${size}px;height:${size}px;font-size:${size * 0.32}px`}
    aria-hidden="true"
  >
    {#if names[user]}{initials(names[user])}{:else}<Icon name={isExtension(user) ? "phone" : "mobile"} size={size * 0.45} />{/if}
    {#if status}<span class={["absolute -right-px -bottom-px size-[11px] rounded-full ring-[2.5px] ring-surface", status.dot]}></span>{/if}
  </span>
{/snippet}

<div class="
  grid grid-cols-[minmax(250px,_320px)_minmax(0,_1fr)] h-[calc(100dvh_-_11rem)] min-h-[460px] overflow-hidden
  rounded-[8px] border border-border border-t-panel-edge bg-surface shadow-panel
  max-[850px]:grid-cols-[1fr] max-[850px]:h-auto
">
  <!-- Conversation list -->
  <aside class={["flex flex-col min-h-0 border-r border-border max-[850px]:border-r-0 max-[850px]:border-b", selected && "max-[850px]:hidden"]}>
    <div class="flex items-center justify-between gap-2 px-4 pt-4 pb-3">
      <h2 class="m-0 text-[1.05rem] font-semibold">Chats</h2>
      <button class={headerButton} aria-label="New chat" title="New chat" aria-pressed={composing}
        disabled={!account} onclick={() => (composing = !composing)}><Icon name="edit" size={18} /></button>
    </div>
    {#if composing}
      <form
        class="flex gap-2 px-4 pb-3"
        onsubmit={(e) => {
          e.preventDefault();
          if (target.trim()) run(() => openConversation(target.trim()));
        }}
      >
        <!-- svelte-ignore a11y_autofocus -->
        <input class={[ui.input, "px-3 py-2"]} autofocus required aria-label="New chat recipient" placeholder="Extension or SIP address" bind:value={target} />
        <button class={ui.primaryButton} disabled={!target.trim()}>Start</button>
      </form>
    {/if}
    <label class="relative block px-4 pb-3">
      <span class="sr-only">Search chats</span>
      <span class="absolute left-7 top-[1.15rem] -translate-y-1/2 text-muted pointer-events-none"><Icon name="search" size={16} /></span>
      <input class={[ui.input, "pl-9 pr-3 py-2 min-h-[38px] rounded-full"]} placeholder="Search chats" bind:value={search}
        oninput={() => run(refresh)} />
    </label>
    <div class="flex-1 min-h-0 overflow-y-auto px-2 pb-2">
      {#each conversations as c (c.Remote)}
        <button
          class={[
            "flex items-center gap-3 w-full px-2 py-[0.6rem] rounded-[8px] text-left cursor-pointer outline-offset-[-2px] transition-colors duration-100",
            selected === c.Remote ? "bg-accent-surface" : "hover:bg-control-hover",
          ]}
          onclick={() => run(() => openConversation(c.Remote))}
        >
          {@render avatar(c.Remote)}
          <span class="flex flex-col flex-1 min-w-0 gap-[0.15rem]">
            <span class="flex items-baseline gap-2 min-w-0">
              <strong class={["flex-1 truncate text-[0.92rem]", c.Unread ? "font-bold text-foreground" : "font-semibold"]}>{displayName(c.Remote)}</strong>
              <span class={["shrink-0 text-[0.72rem] tabular-nums", c.Unread ? "text-accent-text" : "text-muted"]}>{listTime(c.Updated)}</span>
            </span>
            <span class="flex items-center gap-2 min-w-0">
              <span class={["flex-1 truncate text-[0.8rem]", c.Unread ? "text-foreground" : "text-muted"]}>{c.Preview}</span>
              {#if c.Unread}<span class="shrink-0 min-w-[20px] px-[0.4rem] py-px rounded-full bg-brand text-white text-[0.7rem] font-semibold text-center">{c.Unread}</span>{/if}
            </span>
          </span>
        </button>
      {:else}
        <p class={[ui.empty, "px-4"]}>{search ? "No chats match your search." : "No chats yet. Start one with the pencil button."}</p>
      {/each}
    </div>
  </aside>

  <!-- Selected conversation -->
  <section class={["flex flex-col min-h-0 min-w-0", !selected && "max-[850px]:hidden"]} aria-label="Conversation">
    {#if selected}
      {@const presence = presenceByUser.get(userOf(selected))}
      {@const status = presence ? statusStyle(presence.State) : null}
      <header class="flex items-center gap-3 px-5 py-3 border-b border-border">
        <button class={[headerButton, "hidden max-[850px]:grid"]} aria-label="Back to chats" onclick={() => (selected = "")}
          ><span class="rotate-180"><Icon name="arrow" size={18} /></span></button>
        {@render avatar(selected, 42)}
        <div class="flex-1 min-w-0">
          <h2 class="m-0 text-[1.02rem] font-semibold truncate" title={selected}>{displayName(selected)}</h2>
          <p class="m-0 text-[0.78rem] truncate">
            {#if status}<span class={status.text}>{status.label}</span>{#if presence?.Note}<span class="text-muted"> · {presence.Note}</span>{/if}
            {:else}<span class="text-subtle">{selected}</span>{/if}
          </p>
        </div>
        <button class={headerButton} aria-label="Call" title="Call" disabled={busy} onclick={() => onDial(selected)}><Icon name="phone" size={18} /></button>
        <button class={headerButton} aria-label="Search in chat" title="Search in chat" aria-pressed={showMessageSearch}
          onclick={() => (showMessageSearch = !showMessageSearch)}><Icon name="search" size={18} /></button>
        <button class={headerButton} aria-label="Save as contact" title="Save as contact" disabled={busy} onclick={() => onSave(selected)}><Icon name="user-plus" size={18} /></button>
        <button class={headerButton} aria-label="Delete chat" title="Delete chat" aria-pressed={confirmDelete} onclick={() => (confirmDelete = !confirmDelete)}><Icon name="trash" size={18} /></button>
      </header>
      {#if confirmDelete}
        <div class="flex flex-wrap items-center gap-2 px-5 py-3 border-b border-border bg-[#2c2027]">
          <span class="mr-auto text-[0.9rem]">Delete this chat from this device?</span>
          <button class={ui.dangerButton}
            onclick={() =>
              run(async () => {
                const deletingAccount = account,
                  remote = selected,
                  revision = viewRevision;
                await api.DeleteConversation(deletingAccount, remote);
                if (disposed || account !== deletingAccount || revision !== viewRevision) return;
                viewRevision++;
                selected = "";
                messages = [];
                confirmDelete = false;
                await refresh();
              })}>Delete</button
          ><button class={ui.button} onclick={() => (confirmDelete = false)}>Cancel</button>
        </div>
      {/if}
      {#if showMessageSearch}
        <form
          class="flex gap-2 px-5 py-3 border-b border-border"
          onsubmit={(e) => {
            e.preventDefault();
            viewRevision++;
            messages = [];
            loadingOlder = false;
            run(refresh);
          }}
        >
          <input class={[ui.input, "px-3 py-2"]} aria-label="Search messages" placeholder="Search this chat" bind:value={messageSearch} />
          <button class={ui.button}>Search</button>
        </form>
      {/if}

      <div class="flex-1 min-h-0 overflow-y-auto px-5 py-4 bg-surface-inset" bind:this={thread} aria-label="Conversation messages">
        {#if hasOlder}<div class="flex justify-center mb-4">
            <button class={[ui.quietButton, "rounded-full text-[0.8rem] min-h-[30px] py-1"]} disabled={busy} onclick={() => run(older)}>Load earlier messages</button>
          </div>{/if}
        {#each messages as m, i (m.ID)}
          {@const previous = messages[i - 1]}
          {@const next = messages[i + 1]}
          {@const outgoing = m.Direction === "outgoing"}
          {@const newDay = !previous || !sameDay(previous.Created, m.Created)}
          {@const grouped = !newDay && continues(previous, m)}
          {@const last = !next || !sameDay(m.Created, next.Created) || !continues(m, next)}
          {#if newDay}
            <div class="flex items-center gap-3 my-4 text-[0.72rem] text-muted first:mt-0">
              <span class="flex-1 h-px bg-border"></span>{dayLabel(m.Created)}<span class="flex-1 h-px bg-border"></span>
            </div>
          {/if}
          <div class={["flex flex-col", outgoing ? "items-end" : "items-start", grouped ? "mt-[3px]" : "mt-3"]}>
            <div
              class={[
                "max-w-[min(75%,_560px)] px-[0.85rem] py-[0.5rem] text-[0.92rem] leading-[1.45] whitespace-pre-wrap wrap-anywhere shadow-[0_1px_1px_#0003]",
                outgoing ? "bg-brand text-white rounded-[16px]" : "bg-control text-foreground rounded-[16px]",
                outgoing && !last && "rounded-br-[6px]",
                outgoing && grouped && "rounded-tr-[6px]",
                !outgoing && !last && "rounded-bl-[6px]",
                !outgoing && grouped && "rounded-tl-[6px]",
                m.Status === "failed" && "bg-[#4a2630] text-[#ffd6dd]",
              ]}
            >{m.Body}</div>
            {#if last || m.Status === "failed"}
              <span class={["flex items-center gap-2 mt-1 px-1 text-[0.7rem] tabular-nums", m.Status === "failed" ? "text-danger" : "text-muted"]}>
                {timeLabel(m.Created)}{#if outgoing} · {m.Status === "accepted by server" ? "Sent" : m.Status === "failed" ? "Not sent" : m.Status}{/if}
                {#if m.Status === "failed"}<button class="underline cursor-pointer" onclick={() => (draft = m.Body)}>Edit and resend</button>{/if}
              </span>
            {/if}
          </div>
        {:else}
          <div class="grid place-items-center h-full text-center">
            <div>
              {@render avatar(selected, 64)}
              <p class={[ui.hint, "mt-3"]}>{messageSearch ? "No messages match your search." : `This is the start of your chat with ${displayName(selected)}.`}</p>
            </div>
          </div>
        {/each}
      </div>

      {#if messaging === "sip"}
        <form
          class="flex items-end gap-2 px-4 py-3 border-t border-border"
          onsubmit={(e) => {
            e.preventDefault();
            if (!busy && draft.trim()) run(send);
          }}
        >
          <textarea
            bind:this={composer}
            class={[ui.input, "flex-1 min-h-[42px] max-h-[160px] px-4 py-[0.6rem] rounded-[21px] resize-none leading-[1.45]"]}
            rows="1"
            required
            aria-label="Message"
            placeholder="Write a message"
            maxlength="16384"
            bind:value={draft}
            onkeydown={(e) => {
              // Enter sends; Shift+Enter starts a new line.
              if (e.key === "Enter" && !e.shiftKey && !e.isComposing) {
                e.preventDefault();
                if (!busy && draft.trim()) run(send);
              }
            }}
          ></textarea>
          <button
            class="grid place-items-center shrink-0 size-[42px] rounded-full bg-brand text-white cursor-pointer outline-offset-2 transition-colors duration-100 enabled:hover:bg-brand-hover disabled:opacity-40 disabled:cursor-not-allowed"
            aria-label="Send"
            disabled={busy || !account || !draft.trim()}><Icon name="send" size={18} /></button>
        </form>
        <p class="m-0 px-5 pb-2 text-[0.7rem] text-muted">Enter sends · Shift+Enter adds a line · "Sent" means the server accepted the message.</p>
      {:else}
        <p class="m-0 px-5 py-4 border-t border-border text-[0.85rem] text-muted">
          {messaging === "disabled"
            ? "Chat is disabled for this account."
            : currentAccount?.Capabilities?.SwyxDetected
              ? "This Swyx server does not accept SIP chat messages. Swyx chat runs on Swyx Messenger, which Voiper cannot reach yet; received messages stay readable here."
              : "This server does not accept SIP chat messages."}
        </p>
      {/if}
    {:else}
      <div class="grid place-items-center flex-1 p-8 text-center">
        <div class="flex flex-col items-center gap-3">
          <span class="grid place-items-center size-[64px] rounded-full bg-accent-surface text-accent-text"><Icon name="messages" size={28} /></span>
          <h3 class={ui.h3}>Your chats</h3>
          <p class={ui.hint}>Choose a chat or start a new one with the pencil button.</p>
        </div>
      </div>
    {/if}
  </section>
</div>
