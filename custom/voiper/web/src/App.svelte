<script>
  import { ui } from "./ui.js";
  import { Switch } from "bits-ui";
  import { onMount, tick } from "svelte";
  import * as api from "../wailsjs/go/app/App.js";
  import { EventsOn } from "../wailsjs/runtime/runtime.js";
  import Accounts from "./Home/Home.svelte";
  import Overview from "./Overview.svelte";
  import Phone from "./Phone/Phone.svelte";
  import Messages from "./Messages/Messages.svelte";
  import History from "./History/History.svelte";
  import Directory from "./Contacts/Directory.svelte";
  import CSVFields from "./Contacts/CSVFields.svelte";
  import AudioSettings from "./Audio/AudioSettings.svelte";
  import LiveDiagnostics from "./components/LiveDiagnostics.svelte";
  import Dropdown from "./components/Dropdown.svelte";
  import Sidebar from "./components/Sidebar.svelte";
  import Icon from "./components/Icon.svelte";
  import ContactCard from "./Contacts/ContactCard.svelte";
  import { extensionOf } from "./Contacts/presence.js";

  let page = $state("overview");
  $effect(() => {
    const selectedPage = page;
    tick().then(() => {
      if (page === selectedPage) window.scrollTo({ top: 0, left: 0 });
    });
  });
  let confirmQuit = $state(false);
  let quitDialog;
  $effect(() => {
    if (quitDialog) {
      if (confirmQuit && !quitDialog.open) quitDialog.showModal();
      else if (!confirmQuit && quitDialog.open) quitDialog.close();
    }
  });
  let snapshot = $state({ Accounts: [], Calls: [], DND: false, Audio: {} });
  let dndPending = $state(false);
  let dndRevision = 0;
  let account = $state("");
  let accountOptions = $state([]);
  let accountInitialized = false;
  let target = $state("");
  let error = $state("");
  let notice = $state("");
  let busy = $state(false);
  let contacts = $state([]);

  let search = $state("");
  let contact = $state({
    ID: 0,
    Name: "",
    Address: "",
    Notes: "",
    Favorite: false,
  });

  let devices = $state([]);
  let audio = $state({
    Backend: "",
    InputDevice: "",
    OutputDevice: "",
    RingerDevice: "",
  });
  let preferences = $state({
    DefaultAccount: "",
    Notifications: true,
    CloseToBackground: false,
    Autostart: false,
    MessageRetentionDays: 0,
    KeepStatusWhenLocked: false,
    ScreenLockCommand: "",
  });
  let desktopCapabilities = $state({});
  let globalShortcuts = $state({
    Available: false,
    Enabled: false,
    Configuring: false,
    Bindings: [],
  });
  let shortcutBusy = $state(false);
  let preferencesLoaded = false;
  let csv = $state("");
  let rawCSV = $state("");
  let mapCSV = $state(false);
  let importFormat = $state("csv");
  let importPreview = $state(null);
  let importSource = $state(null);
  let importRevision = 0;
  let mergeImportNumbers = $state(false);
  let logs = $state([]);
  let showDiagnostics = $state(false);
  let refreshing = false;
  let disposed = false;

  function clearImportPreview() {
    importRevision++;
    importPreview = null;
    importSource = null;
  }
  async function previewContacts() {
    clearImportPreview();
    if (!csv) return;
    const revision = importRevision;
    const source = {
      format: importFormat,
      text: csv,
      merge: mergeImportNumbers,
    };
    const preview = await api.PreviewContactImport(
      source.format,
      source.text,
      source.merge,
    );
    if (revision !== importRevision) return;
    importSource = source;
    importPreview = preview;
  }

  async function refresh() {
    if (refreshing || disposed) return;
    refreshing = true;
    try {
      const requestedDNDRevision = dndRevision;
      const next = await api.Snapshot();
      // Presence comes from a Go map; stable ordering avoids false UI changes.
      next.Presence?.sort((a, b) =>
        `${a.Account}:${a.Target}:${a.SubscriptionID}`.localeCompare(`${b.Account}:${b.Target}:${b.SubscriptionID}`),
      );
      for (const field of ["Accounts", "Presence", "Audio", "DND", "UnreadMessages"]) {
        // A snapshot started before a toggle must not undo its local feedback.
        if (field === "DND" && (dndPending || requestedDNDRevision !== dndRevision)) continue;
        if (JSON.stringify(next[field]) !== JSON.stringify(snapshot[field]))
          snapshot[field] = next[field];
      }
      // Registration retries must not rebuild account menus while they are open.
      const nextAccountOptions = next.Accounts.map((a) => ({ value: a.Name, label: a.Name }));
      if (JSON.stringify(nextAccountOptions) !== JSON.stringify(accountOptions))
        accountOptions = nextAccountOptions;
      // Active calls also drive elapsed time and their live media meters.
      if (next.Calls.length || snapshot.Calls.length) snapshot.Calls = next.Calls;
      if (!preferencesLoaded) {
        await loadPreferences();
        await acceptDialTarget();
      }
      if (!accountInitialized && snapshot.Accounts.length) {
        if (!account) account = snapshot.Accounts[0].Name;
        accountInitialized = true;
      }
    } catch (e) {
      error = String(e);
    } finally {
      refreshing = false;
    }
  }
  async function setDND(checked) {
    if (dndPending) return;
    const previous = snapshot.DND;
    dndPending = true;
    dndRevision++;
    snapshot.DND = checked;
    error = "";
    try {
      await api.SetDND(checked);
    } catch (e) {
      snapshot.DND = previous;
      error = String(e);
    } finally {
      dndRevision++;
      dndPending = false;
    }
  }
  async function run(action) {
    busy = true;
    error = "";
    notice = "";
    try {
      return await action();
    } catch (e) {
      error = String(e);
    } finally {
      busy = false;
      await refresh();
    }
  }
  let contactsRevision = 0;
  async function loadContacts(more = false) {
    // A slower earlier search must not replace the results of a newer one.
    const revision = ++contactsRevision;
    const rows = await api.ContactsPage(
      search,
      more ? contacts.length : 0,
      100,
    );
    if (revision === contactsRevision)
      contacts = more ? [...contacts, ...rows] : rows;
  }
  // Searching follows typing; the timer collapses bursts of keystrokes.
  let searchTimer;
  function searchContacts() {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(() => {
      loadContacts().catch((e) => (error = String(e)));
    }, 200);
  }
  let presenceByTarget = $derived(
    new Map(
      (snapshot.Presence || [])
        .filter((p) => p.Account === account && !p.Voicemail)
        .map((p) => [p.Target, p]),
    ),
  );
  let accountRegistered = $derived(
    snapshot.Accounts.some((a) => a.Name === account && a.State === "registered"),
  );
  // Displayed colleagues are watched automatically; Go bounds and reuses subscriptions.
  $effect(() => {
    if (page !== "contacts" || !account || !accountRegistered) return;
    const extensions = contacts.map(extensionOf).filter(Boolean);
    if (extensions.length)
      api.WatchVisiblePresence(account, extensions).catch((e) => (error = String(e)));
  });
  async function navigate(next) {
    page = next;
    await run(async () => {
      if (next === "contacts") await loadContacts();
      if (next === "settings") {
        await loadPreferences();
        await loadShortcuts();
        audio = { ...snapshot.Audio };
        devices = await api.AudioDevices();
      }
    });
  }
  function dial(address) {
    target = address;
    page = "phone";
  }
  function saveCorrespondent(accountName, remote) {
    contact = {
      ID: 0,
      Name: "",
      Address: remote,
      Notes: "",
      Favorite: false,
      Numbers: [],
      Account: accountName,
    };
    navigate("contacts");
  }
  async function loadPreferences() {
    const settings = await api.DesktopSettings();
    preferences = settings.Preferences;
    preferencesLoaded = true;
    desktopCapabilities = settings.Capabilities;
    if (!accountInitialized && preferences.DefaultAccount) {
      account = preferences.DefaultAccount;
      accountInitialized = true;
    }
  }
  async function acceptDialTarget() {
    const address = await api.TakeDialTarget();
    if (address) dial(address);
  }
  async function loadShortcuts() {
    globalShortcuts = await api.GetGlobalShortcuts();
  }
  async function shortcutAction(action) {
    shortcutBusy = true;
    error = "";
    try {
      await action();
    } catch (e) {
      error = String(e);
    } finally {
      try {
        await loadShortcuts();
      } catch (e) {
        error = String(e);
      }
      shortcutBusy = false;
    }
  }
  async function keyboard(event) {
    if (event.key === "Escape" && showDiagnostics) {
      showDiagnostics = false;
      document.getElementById("diagnostics-toggle")?.focus();
      return;
    }
    if (!(event.ctrlKey || event.metaKey) || event.altKey || event.shiftKey)
      return;
    const pages = [
      "phone",
      "contacts",
      "history",
      "messages",
      "accounts",
      "settings",
    ];
    const index = Number(event.key) - 1;
    if (index >= 0 && index < pages.length) {
      event.preventDefault();
      await navigate(pages[index]);
    }
    if (event.key.toLowerCase() === "k") {
      event.preventDefault();
      page = "phone";
      await tick();
      document.querySelector(".dial-input")?.focus();
    }
  }
  onMount(() => {
    disposed = false;
    const off = [
      EventsOn("phone-changed", refresh),
      EventsOn("confirm-quit", () => (confirmQuit = true)),
      EventsOn("desktop-ready", () =>
        run(async () => {
          await loadPreferences();
          await acceptDialTarget();
          await loadShortcuts();
        }),
      ),
      EventsOn("desktop-warning", (text) => (notice = String(text))),
      EventsOn("shortcuts-changed", () =>
        loadShortcuts().catch((e) => (error = String(e))),
      ),
      EventsOn("show-dialer", async () => {
        page = "phone";
        await tick();
        document.querySelector(".dial-input")?.focus();
      }),
      EventsOn("dial-target", () => run(acceptDialTarget)),
      EventsOn("phone-error", (e) => (error = String(e))),
      EventsOn("incoming-call", (remote) => {
        notice = `Incoming call from ${remote.Remote || remote}`;
        page = "phone";
      }),
      EventsOn(
        "incoming-message",
        (remote) => (notice = `Message from ${remote.Remote || remote}`),
      ),
      EventsOn("log", (line) => {
        logs = [...logs.slice(-499), String(line)];
      }),
    ];
    run(async () => {
      await loadPreferences();
      await acceptDialTarget();
    });
    const interval = setInterval(refresh, 1000);
    return () => {
      disposed = true;
      clearInterval(interval);
      clearTimeout(searchTimer);
      off.forEach((stop) => stop());
    };
  });
</script>

<svelte:window onkeydown={keyboard} />
<svelte:head><title>Voiper</title></svelte:head>
<dialog class="
  p-6 m-auto max-w-[min(32rem,_90vw)] rounded-[6px] bg-surface [box-shadow:0_16px_64px_#0009] text-inherit
  border border-border [&::backdrop]:bg-[#0009] [&>.row]:mt-4
"
  bind:this={quitDialog}
  oncancel={() => (confirmQuit = false)}
  onclose={() => (confirmQuit = false)}
>
  <h2 class={ui.h2}>End active calls?</h2>
  <p class={ui.text}>Quitting ends all calls and unregisters your accounts.</p>
  <div class={ui.row}>
    <button class={ui.button} onclick={() => (confirmQuit = false)}>Keep open</button><button
      class={ui.dangerButton}
      onclick={() => api.Quit()}>Quit Voiper</button
    >
  </div>
</dialog>
<div class="grid grid-cols-[190px_minmax(0,_1fr)] min-h-screen w-full max-[1100px]:grid-cols-[64px_minmax(0,_1fr)]">
  <Sidebar {page} {navigate} unreadMessages={snapshot.UnreadMessages} callCount={snapshot.Calls.length} />
  <main class="
    px-[clamp(1rem,_2.5vw,_3rem)] pt-7 pb-20 min-w-0 max-[700px]:pt-5 *:min-w-0
    [@media(max-height:_550px)]:pt-4 [&>.panel+.panel]:mt-6
  ">
    <header class="
      flex items-center justify-between gap-6 mb-6 max-[700px]:items-start max-[700px]:flex-wrap
      max-[700px]:gap-4 [@media(max-height:_550px)]:mb-4 max-[850px]:flex-wrap
    ">
      <div>
        <h1 class="m-0 text-2xl font-semibold tracking-[-0.025em] wrap-anywhere">
          {{
            overview: "Overview",
            phone: "Phone",
            contacts: "Your contacts",
            history: "Recent calls",
            messages: "Messages",
            accounts: "SIP accounts",
            settings: "Audio & settings",
            logs: "Diagnostics",
          }[page]}
        </h1>
      </div>
    <div class="ml-auto max-[850px]:ml-0">
      <div class="flex items-center gap-[0.8rem] text-[0.85rem] text-muted">
        <label class={ui.label} for="do-not-disturb">Do not disturb</label>
        <Switch.Root id="do-not-disturb" class={[ui.control, `
          relative w-[36px] h-[22px] min-h-[22px] p-[2px] rounded-[6px] border border-[#626d7b]
          bg-control shadow-[inset_0_1px_3px_#0004] font-medium text-inherit
          enabled:hover:bg-control-hover enabled:hover:border-control-border-hover
          data-[state=checked]:bg-brand data-[state=checked]:border-accent
          data-[state=checked]:enabled:hover:bg-brand data-[state=checked]:enabled:hover:border-accent
        `]}
          checked={snapshot.DND}
          aria-busy={dndPending}
          onCheckedChange={setDND}>
          <Switch.Thumb class="
            relative left-0 block size-[16px] rounded-[3px] bg-[#f0f3f7] shadow-[0_1px_3px_#0006]
            transition-[left] duration-100 data-[state=checked]:left-[14px]
          " />
        </Switch.Root>
      </div>
    </div>
      {#if ["overview", "phone", "contacts", "messages"].includes(page)}
        <label class={[ui.label, "w-[280px] max-w-[50%] max-[850px]:w-full max-[850px]:max-w-none"]}
          >Outgoing account<Dropdown
            label="Outgoing account"
            bind:value={account}
            options={[
              { value: "", label: "Choose account" },
              ...accountOptions,
            ]}
          /></label
        >
      {/if}
    </header>
    {#if error}<div class="flex items-center justify-between mb-4 px-4 py-3 rounded-[6px] border border-[#814351] bg-[#351e28]" role="alert">
        <span>{error}</span><button class={ui.iconButton}
          aria-label="Dismiss error"
          onclick={() => (error = "")}>×</button
        >
      </div>{/if}
    {#if notice}<div class="flex items-center justify-between mb-4 px-4 py-3 rounded-[6px] border border-brand-border bg-accent-surface" role="status">
        <span>{notice}</span><button class={ui.iconButton}
          aria-label="Dismiss notice"
          onclick={() => (notice = "")}>×</button
        >
      </div>{/if}

    {#if page === "overview"}
      <Overview {snapshot} bind:account bind:target {run} {busy} manageContacts={() => navigate("contacts")} />
    {:else if page === "phone"}
      <Phone {snapshot} bind:account bind:target {run} {busy} />
    {:else if page === "accounts"}
      <Accounts {snapshot} {run} {busy} />
    {:else if page === "contacts"}
      <div class="grid grid-cols-[minmax(270px,_1fr)_minmax(320px,_1.2fr)] gap-6 max-[1000px]:grid-cols-[1fr] *:min-w-0">
        <section class={ui.panel}>
          <div class="flex items-baseline justify-between gap-3 pb-4 mb-4 border-b border-border">
            <h2 class="m-0 text-[1.15rem] font-semibold">Phonebook</h2>
            <span class="text-[0.78rem] text-muted tabular-nums">{contacts.length}{contacts.length >= 100 ? "+" : ""} shown</span>
          </div>
          <label class="relative block">
            <span class="absolute left-3 top-1/2 -translate-y-1/2 text-muted pointer-events-none"><Icon name="search" size={17} /></span>
            <input class={[ui.input, "pl-10 pr-3 py-[0.65rem]"]}
              type="search"
              aria-label="Search contacts"
              placeholder="Search names, extensions or numbers"
              bind:value={search}
              oninput={searchContacts}
            />
          </label>
          <div class="flex flex-wrap items-center gap-x-4 gap-y-1 mt-3 text-[0.75rem] text-muted">
            <span class="inline-flex items-center gap-[0.4rem]"><span class="size-[10px] rounded-full bg-accent-surface border border-brand-border"></span>Internal colleague</span>
            <span class="inline-flex items-center gap-[0.4rem]"><span class="size-[10px] rounded-full bg-[#2c3640] border border-[#465564]"></span>Mobile / external</span>
          </div>
          <div class="mt-2" aria-live="polite">
            {#each contacts as c (c.ID)}
              {@const extension = extensionOf(c)}
              <ContactCard
                contact={c}
                presence={extension ? presenceByTarget.get(extension) : undefined}
                {busy}
                onDial={(address) => {
                  if (c.Account) account = c.Account;
                  dial(address);
                }}
                onMessage={() => {
                  if (c.Account) account = c.Account;
                  target = extension || c.Address;
                  page = "messages";
                }}
                onEdit={() =>
                  (contact = {
                    ...c,
                    Numbers: (c.Numbers || []).map((n) => ({ ...n })),
                  })}
                onDelete={() =>
                  run(async () => {
                    await api.DeleteContact(c.ID);
                    await loadContacts();
                  })}
              />
            {:else}<p class={ui.empty}>
                {search.trim() ? "No contacts match this search." : "Add a contact or import your phonebook."}
              </p>{/each}
          </div>
          {#if contacts.length >= 100}<button class={[ui.button, "w-full mt-4"]}
              disabled={busy}
              onclick={() => run(() => loadContacts(true))}
              >Load more contacts</button
            >{/if}
          <p class="m-0 mt-4 leading-[1.6] text-muted text-[0.78rem]">
            {account
              ? `Status of internal colleagues is shown for ${account}.`
              : "Choose an outgoing account to see colleague status."}
            Imported directory entries remain available offline.
          </p>
        </section>
        <section class={ui.panel}>
          <h2 class={ui.h2}>{contact.ID ? "Edit contact" : "New contact"}</h2>
          {#if contact.Source === "directory-sync"}<p class={ui.hint}>
              Saving changes keeps your own copy. Directory refreshes will
              preserve it.
            </p>{/if}
          {#if contact.DirectoryID && contact.Source !== "directory-sync"}
            <p class={ui.hint}>
              This is your local copy of a directory contact.
            </p>
            <button class={ui.button}
              disabled={busy}
              onclick={() =>
                run(async () => {
                  await api.RestoreDirectoryContact(contact.ID);
                  contact = {
                    ID: 0,
                    Name: "",
                    Address: "",
                    Notes: "",
                    Favorite: false,
                  };
                  notice =
                    "Directory name and numbers restored. Personal notes and favorites kept.";
                  await loadContacts();
                })}>Restore directory name and numbers</button
            >
          {/if}
          <form
            class={ui.stack}
            onsubmit={(e) => {
              e.preventDefault();
              run(async () => {
                await api.SaveContact(contact);
                contact = {
                  ID: 0,
                  Name: "",
                  Address: "",
                  Notes: "",
                  Favorite: false,
                };
                await loadContacts();
              });
            }}
          >
            <label class={ui.label}>Name<input class={ui.field} bind:value={contact.Name} /></label><label class={ui.label}>Company or team<input class={ui.field} bind:value={contact.Company} /></label><label class={ui.label}
              >Number or SIP address<input class={ui.field}
                required
                bind:value={contact.Address}
              /></label
            >
            <div class={ui.stack}>
              <label class={ui.label}
                >Preferred account<Dropdown
                  label="Preferred account"
                  bind:value={contact.Account}
                  options={[
                    { value: "", label: "Use selected account" },
                    ...accountOptions,
                  ]}
                /></label
              >
              {#each contact.Numbers || [] as number, i}<div class={ui.row}>
                  <input class={ui.field}
                    aria-label="Number label"
                    placeholder="Mobile / work"
                    bind:value={number.Label}
                  /><input class={ui.field}
                    required
                    aria-label="Additional number"
                    placeholder="Number or SIP address"
                    bind:value={number.Address}
                  /><button class={ui.button}
                    type="button"
                    onclick={() =>
                      (contact.Numbers = contact.Numbers.filter(
                        (_, index) => index !== i,
                      ))}>Remove</button
                  >
                </div>{/each}
              <button class={ui.button}
                type="button"
                onclick={() =>
                  (contact.Numbers = [
                    ...(contact.Numbers || []),
                    { Label: "", Address: "" },
                  ])}>Add another number</button
              >
            </div>
            <label class={ui.label}>Notes<textarea class="
              px-[0.8rem] py-[0.65rem] outline-offset-3 min-w-0 w-full min-h-[90px] rounded-[6px] bg-field
              text-foreground resize-y border border-field-border placeholder:text-[#858d99] focus:outline-2
              focus:outline-solid focus:outline-accent focus:border-transparent
            " bind:value={contact.Notes}></textarea></label
            ><label class={ui.checkLabel}
              ><input class={ui.checkbox} type="checkbox" bind:checked={contact.Favorite} /> Favorite</label
            >
            <div class={ui.row}>
              <button class={ui.primaryButton} type="submit" disabled={busy}
                >Save contact</button
              ><button class={ui.button}
                type="button"
                onclick={() =>
                  (contact = {
                    ID: 0,
                    Name: "",
                    Address: "",
                    Notes: "",
                    Favorite: false,
                  })}>Clear</button
              >
            </div>
          </form>
          <details class={[ui.details, ui.section]}>
            <summary class={ui.summary}>Import / export contacts</summary>
            <p class={ui.hint}>
              Import a Swyx phonebook export (XML), vCard 3.0/4.0 or CSV with
              columns name, address, notes and optional group and label. Swyx
              imports use the extension with status as the primary number.
              Contacts match by exact primary address, never by name, and are
              saved in contacts.toml in your Voiper configuration folder.
            </p>
            <input class={ui.field}
              aria-label="CSV, vCard or Swyx XML file"
              type="file"
              disabled={busy}
              accept=".csv,.vcf,.vcard,.xml,text/csv,text/vcard,text/xml,application/xml"
              onchange={(e) =>
                run(async () => {
                  const f = e.currentTarget.files?.[0];
                  csv = "";
                  rawCSV = "";
                  mapCSV = false;
                  clearImportPreview();
                  if (f) {
                    if (f.size > 4194304) throw Error("File exceeds 4 MiB");
                    importFormat = /\.(vcf|vcard)$/i.test(f.name)
                      ? "vcard"
                      : /\.xml$/i.test(f.name)
                        ? "xml"
                        : "csv";
                    csv = await f.text();
                    if (importFormat === "csv") {
                      rawCSV = csv;
                      const columns = await api.CSVColumns(rawCSV);
                      mapCSV = !columns.some(
                        (column) => column.toLowerCase() === "address",
                      );
                      if (mapCSV) {
                        csv = "";
                        notice =
                          "Choose the name and number columns before previewing this CSV.";
                        return;
                      }
                    }
                    await previewContacts();
                  }
                })}
            />
            {#if importFormat === "csv" && rawCSV}
              {#if mapCSV}
                <CSVFields
                  disabled={busy}
                  text={rawCSV}
                  onMapped={async (mapped) => {
                    csv = mapped;
                    clearImportPreview();
                    if (mapped) await previewContacts();
                  }}
                />
              {:else}
                <button class={ui.button}
                  type="button"
                  disabled={busy}
                  onclick={() => {
                    mapCSV = true;
                    clearImportPreview();
                  }}>Map CSV columns</button
                >
              {/if}
            {/if}
            <label class={ui.label}>
              Existing contacts
              <Dropdown
                label="Existing contacts"
                disabled={busy}
                bind:value={mergeImportNumbers}
                options={[
                  { value: false, label: "Keep existing contacts unchanged" },
                  {
                    value: true,
                    label: "Add missing numbers to existing contacts",
                  },
                ]}
                onchange={() => {
                  clearImportPreview();
                  if (csv) run(previewContacts);
                }}
              />
            </label>
            <p class={ui.hint}>
              Existing names, notes, labels, favorites and account preferences
              are always kept. Repeated primary addresses in this file use the
              first record.
            </p>
            {#if importPreview}
              <p class={ui.text} aria-live="polite">
                {importPreview.Added} new · {importPreview.Updated} updated ·
                {importPreview.Skipped} skipped · {importPreview.Invalid} invalid
              </p>
              {#if importPreview.Invalid}
                <p class={ui.error}>
                  Nothing will be imported until all invalid records are fixed.
                </p>
                <ul>
                  {#each importPreview.Entries.filter((entry) => entry.Action === "invalid").slice(0, 20) as entry}
                    <li>Record {entry.Record}: {entry.Detail}</li>
                  {/each}
                </ul>
                {#if importPreview.Invalid > 20}
                  <p class={ui.hint}>
                    Showing the first 20 validation errors.
                  </p>
                {/if}
              {/if}
              <div class="p-4 whitespace-pre-wrap wrap-anywhere max-h-[180px] overflow-auto text-xs bg-field rounded-[6px]">
                <table class="w-full border-collapse text-left">
                  <thead
                    ><tr><th class="p-[0.7rem] border-b border-border align-top font-semibold text-muted">Record</th><th class="p-[0.7rem] border-b border-border align-top font-semibold text-muted">Contact</th><th class="p-[0.7rem] border-b border-border align-top font-semibold text-muted">Action</th></tr
                    ></thead
                  >
                  <tbody>
                    {#each importPreview.Entries.slice(0, 50) as entry}
                      <tr>
                        <td class="p-[0.7rem] border-b border-border align-top">{entry.Record}</td>
                        <td class="p-[0.7rem] border-b border-border align-top">
                          {entry.Contact.Name}<br />{entry.Contact.Address}
                          {#if entry.Contact.Numbers?.length}
                            <br /><span class="text-muted text-[0.78rem]">
                              {entry.Contact.Numbers.map((number) =>
                                number.Label
                                  ? `${number.Label}: ${number.Address}`
                                  : number.Address,
                              ).join(" · ")}
                            </span>
                          {/if}
                        </td>
                        <td class="p-[0.7rem] border-b border-border align-top"
                          >{entry.Action}<br /><span class="text-muted text-[0.78rem]"
                            >{entry.Detail}</span
                          ></td
                        >
                      </tr>
                    {/each}
                  </tbody>
                </table>
              </div>
              {#if importPreview.Entries.length > 50}
                <p class={ui.hint}>
                  Showing the first 50 records; totals cover the entire file.
                </p>
              {/if}
              <button class={ui.button}
                disabled={busy ||
                  importPreview.Invalid > 0 ||
                  importPreview.Added + importPreview.Updated === 0}
                onclick={() =>
                  run(async () => {
                    const source = importSource;
                    if (!source) return;
                    const result = await api.ImportContactFile(
                      source.format,
                      source.text,
                      source.merge,
                    );
                    importPreview = result;
                    if (!result.Committed) return;
                    csv = "";
                    rawCSV = "";
                    mapCSV = false;
                    clearImportPreview();
                    notice = `Imported ${result.Added} contacts, added numbers to ${result.Updated}, skipped ${result.Skipped}`;
                    await loadContacts();
                  })}>Confirm import</button
              >
              <p class={ui.hint}>
                Import checks the current phonebook again; final counts may
                change.
              </p>
            {/if}<button class={ui.button} onclick={() => run(() => api.ExportContacts("csv"))}
              >Export CSV</button
            ><button class={ui.button} onclick={() => run(() => api.ExportContacts("vcard"))}
              >Export vCard</button
            >
          </details>
          <Directory onChanged={() => loadContacts()} />
        </section>
      </div>
    {:else if page === "history"}
      <History
        {account}
        {run}
        {busy}
        onDial={(h) => {
          account = h.Account;
          dial(h.Remote);
        }}
        onSave={(h) => saveCorrespondent(h.Account, h.Remote)}
      />
    {:else if page === "messages"}
      <Messages
        {account}
        {snapshot}
        bind:target
        {run}
        {busy}
        onDial={dial}
        onSave={(remote) => saveCorrespondent(account, remote)}
      />
    {:else if page === "settings"}
      <div class="
        grid grid-cols-[minmax(0,_1.15fr)_minmax(0,_1fr)] gap-6 items-start max-[900px]:grid-cols-[1fr]
        *:min-w-0
      ">
        <AudioSettings
          bind:audio
          bind:devices
          {busy}
          {run}
          callCount={snapshot.Calls.length}
        />
        <section class={[ui.panel, "[&>h3]:pt-5 [&>h3]:border-t [&>h3]:border-border [&_form>button]:self-start [&>:not(:first-child)]:mt-5"]}>
          <h2 class={ui.h2}>Desktop & privacy</h2>
          <form
            class={ui.stack}
            onsubmit={(e) => {
              e.preventDefault();
              run(() => api.SetPreferences(preferences));
            }}
          >
            <label class={ui.label}
              >Default outgoing account<Dropdown
                label="Default outgoing account"
                bind:value={preferences.DefaultAccount}
                options={[
                  { value: "", label: "First enabled account" },
                  ...accountOptions,
                ]}
              /></label
            >
            <label class={ui.checkLabel}
              ><input class={ui.checkbox}
                type="checkbox"
                bind:checked={preferences.Notifications}
              /> Desktop call and message notifications</label
            >
            <label class={ui.checkLabel}
              ><input class={ui.checkbox}
                type="checkbox"
                bind:checked={preferences.CloseToBackground}
                disabled={!desktopCapabilities.Tray}
              /> Closing the window keeps Voiper in the tray</label
            >
            <label class={ui.checkLabel}
              ><input class={ui.checkbox} type="checkbox" bind:checked={preferences.Autostart} /> Start
              Voiper when I log in</label
            >
            <label class={ui.label}
              >Keep message history<Dropdown
                label="Keep message history"
                bind:value={preferences.MessageRetentionDays}
                options={[
                  { value: 0, label: "Until I delete it" },
                  { value: 30, label: "30 days" },
                  { value: 90, label: "90 days" },
                  { value: 365, label: "1 year" },
                ]}
              /></label
            >
            <label class={ui.checkLabel}
              ><input class={ui.checkbox}
                type="checkbox"
                checked={!preferences.KeepStatusWhenLocked}
                onchange={(e) => (preferences.KeepStatusWhenLocked = !e.currentTarget.checked)}
              /> Show me as away while the screen is locked</label
            >
            <label class={ui.label}
              >Screen lock check<input class={ui.field}
                placeholder="Automatic: hyprlock, swaylock, GNOME, KDE, logind"
                disabled={preferences.KeepStatusWhenLocked}
                bind:value={preferences.ScreenLockCommand}
              /></label
            >
            <p class={ui.hint}>
              Leave empty to detect common lock screens. A command replaces the
              detection: exit status 0 means locked, 1 unlocked, for example
              <code>pidof hyprlock</code>.
            </p>
            <p class={ui.hint}>
              Saving a retention limit deletes older messages from this device.
              Autostart uses this installed executable.
            </p>
            <button class={ui.primaryButton} disabled={busy}>Save preferences</button>
          </form>
          <p class={ui.hint}>
            Notifications: {desktopCapabilities.Notifications
              ? "available"
              : "unavailable"} · Tray: {desktopCapabilities.Tray
              ? "available"
              : "unavailable"} · Password wallet: {desktopCapabilities.Secrets
              ? "available"
              : "unavailable"}
          </p>
          {#each desktopCapabilities.Warnings || [] as warning}<p
              class={ui.hint}
            >
              {warning}
            </p>{/each}
          <h3 class={ui.h3}>Global keyboard shortcuts</h3>
          <p class={ui.hint}>
            Enable shortcuts for this session through your desktop's permission
            dialog. Choose keys to show the dialer or toggle mute when exactly
            one call is connected and not held. Shortcuts stop when Voiper
            quits.
          </p>
          <p class={ui.text} role="status">
            {globalShortcuts.Configuring
              ? "Waiting for desktop permission…"
              : globalShortcuts.Enabled
                ? "Enabled for this session"
                : globalShortcuts.Available
                  ? "Available · disabled"
                  : "Unavailable on this desktop"}
          </p>
          {#if globalShortcuts.Error}<p class={ui.hint}>
              {globalShortcuts.Error}
            </p>{/if}
          <div class={[ui.row, "flex-wrap"]}>
            <button class={ui.button}
              disabled={shortcutBusy ||
                globalShortcuts.Enabled ||
                globalShortcuts.Configuring}
              onclick={() => shortcutAction(() => api.EnableGlobalShortcuts())}
              >{globalShortcuts.Available
                ? "Enable shortcuts…"
                : "Check availability / enable…"}</button
            >
            {#if globalShortcuts.Enabled && globalShortcuts.CanConfigure}
              <button class={ui.button}
                disabled={shortcutBusy}
                onclick={() =>
                  shortcutAction(() => api.ConfigureGlobalShortcuts())}
                >Configure keys…</button
              >
            {/if}
            {#if globalShortcuts.Enabled || globalShortcuts.Configuring || shortcutBusy}
              <button class={ui.button}
                onclick={() =>
                  shortcutAction(() => api.DisableGlobalShortcuts())}
                >Disable / cancel setup</button
              >
            {/if}
          </div>
          {#if globalShortcuts.Enabled && !globalShortcuts.CanConfigure}
            <p class={ui.hint}>
              To change keys on this desktop, disable and enable shortcuts
              again.
            </p>
          {/if}
          {#each globalShortcuts.Bindings || [] as binding}
            <p class={ui.text}>
              {binding.Description} · {binding.Trigger ||
                "configured by desktop"}
            </p>
          {/each}
          <button class={ui.button} onclick={() => run(() => api.RequestQuit())}
            >Quit Voiper</button
          >
        </section>
      </div>
    {:else if page === "logs"}
      <LiveDiagnostics {snapshot} {logs} {run} {busy} />
      <section class={ui.panel}>
        <div class={[ui.row, "justify-between flex-wrap"]}>
          <h2 class={ui.h2}>Recent application logs</h2>
          <div class={[ui.row, "flex-wrap"]}>
            <button class={ui.button} onclick={() => run(() => api.ExportDiagnostics(false))}
              >Export diagnostics</button
            >
            <button class={ui.quietButton} onclick={() => (logs = [])}>Clear logs</button
            >
          </div>
        </div>
        <p class={ui.hint}>
          Keeps the latest 500 entries. Call statistics are available in the
          active call view.
        </p>
        <pre class="p-4 whitespace-pre-wrap wrap-anywhere max-h-[60vh] overflow-auto text-xs bg-field rounded-[6px]">{logs.join("\n") || "No log entries."}</pre>
      </section>
    {/if}
  </main>
  {#if showDiagnostics}
    <aside id="live-diagnostics" class="
      fixed z-[105] right-4 bottom-[56px] w-[min(620px,_calc(100vw_-_2rem))] max-h-[calc(100vh_-_110px)]
      overflow-y-auto rounded-[6px] bg-surface [box-shadow:0_12px_40px_#0009]
      [animation:panel-enter_140ms_ease-out] border border-[#586575] [&_.panel]:shadow-none
      [&_.panel]:border-0
    " aria-label="Live diagnostics">
      <div class="px-4 py-2 flex items-center justify-between border-b border-border bg-[#303d49]">
        <span class="text-xs text-muted font-semibold">CONNECTION & CALL QUALITY</span>
        <button class={ui.iconButton} aria-label="Close live diagnostics" title="Close diagnostics"
          onclick={() => { showDiagnostics = false; document.getElementById("diagnostics-toggle")?.focus(); }}>×</button>
      </div>
      <LiveDiagnostics {snapshot} {logs} {run} {busy} />
    </aside>
  {/if}
  <footer class="
    px-[clamp(1rem,_2.5vw,_3rem)] py-[4px] fixed z-[110] bottom-0 left-0 right-0 flex justify-between
    items-center gap-3 min-h-[44px] [transform:translateZ(0)] border-t border-t-field-border
    bg-surface-secondary [box-shadow:0_-1px_4px_#0003]
  " aria-label="Connection status and diagnostics">
    <div class="
      flex items-center gap-[0.65rem] text-xs text-muted wrap-anywhere max-[450px]:gap-[0.3rem]
      max-[450px]:text-[0.7rem]
    ">
      <span class="dot shrink-0 size-[7px] rounded-full bg-[#9c7084] [&.online]:bg-[#73d3a1]" class:online={snapshot.Accounts.some((a) => a.State === "registered")}></span>
      <span>{snapshot.Accounts.filter((a) => a.State === "registered").length}/{snapshot.Accounts.length} accounts connected</span>
      {#if snapshot.Calls.length}<span>· {snapshot.Calls.length} calls</span>{/if}
    </div>
    <div class="flex items-center gap-[0.65rem] max-[450px]:gap-[0.2rem]">
      <button id="diagnostics-toggle" class={ui.iconButton} class:active={showDiagnostics}
        aria-label="Live connection diagnostics" title="Live connection diagnostics"
        aria-expanded={showDiagnostics} aria-controls="live-diagnostics"
        onclick={() => (showDiagnostics = !showDiagnostics)}>
        <svg aria-hidden="true" viewBox="0 0 24 24"><path d="M3 12h4l3-7 4 14 3-7h4" /></svg>
      </button>
      <button class={ui.iconButton} class:active={page === "logs"} aria-label="Open diagnostic logs" title="Diagnostic logs"
        onclick={() => { showDiagnostics = false; navigate("logs"); }}>
        <svg aria-hidden="true" viewBox="0 0 24 24"><rect x="4" y="3" width="16" height="18" rx="2"/><path d="M8 8h8M8 12h8M8 16h5" /></svg>
      </button>
      <button class={ui.iconButton} disabled={busy} aria-label="Export diagnostics" title="Export diagnostics"
        onclick={() => run(() => api.ExportDiagnostics(false))}>
        <svg aria-hidden="true" viewBox="0 0 24 24"><path d="M12 3v12m-4-4 4 4 4-4M4 16v5h16v-5" /></svg>
      </button>
    </div>
  </footer>
</div>
