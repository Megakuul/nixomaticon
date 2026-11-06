<script>
  import { ui } from "../ui.js";
  import { untrack } from "svelte";
  import Dropdown from "../components/Dropdown.svelte";
  import * as api from "../../wailsjs/go/app/App.js";

  let { text, onMapped, disabled = false } = $props();
  let columns = $state([]);
  let mapping = $state({
    Name: "",
    AdditionalName: "",
    Address: "",
    Notes: "",
    Numbers: [],
  });
  let busy = $state(false);
  let error = $state("");
  let notice = $state("");
  let revision = 0;

  $effect(() => {
    const source = text;
    const current = ++revision;
    columns = [];
    error = "";
    notice = "";
    busy = false;
    untrack(() => onMapped(""));
    if (source) {
      busy = true;
      api.CSVColumns(source).then(
        (headers) => {
          if (current !== revision) return;
          columns = headers;
          const canonical = (name) =>
            headers.find((header) => header.toLowerCase() === name) || "";
          mapping = {
            Name: canonical("name"),
            AdditionalName: "",
            Address: canonical("address"),
            Notes: canonical("notes"),
            Numbers: [],
          };
          busy = false;
        },
        (failure) => {
          if (current !== revision) return;
          error = String(failure);
          busy = false;
        },
      );
    }
    return () => {
      revision++;
    };
  });

  function invalidate() {
    revision++;
    notice = "";
    error = "";
    onMapped("");
  }

  function addNumber() {
    mapping.Numbers = [...mapping.Numbers, { Column: "", Label: "" }];
    invalidate();
  }

  function removeNumber(index) {
    mapping.Numbers = mapping.Numbers.filter(
      (_, position) => position !== index,
    );
    invalidate();
  }

  async function apply() {
    const current = revision;
    busy = true;
    error = "";
    notice = "";
    try {
      const canonical = await api.MapContactCSV(text, $state.snapshot(mapping));
      if (current !== revision) return;
      await onMapped(canonical);
      if (current === revision)
        notice = "Columns mapped. Review the import preview before saving.";
    } catch (failure) {
      if (current === revision) error = String(failure);
    } finally {
      if (current === revision) busy = false;
    }
  }
</script>

<fieldset
  class="min-w-0 rounded-lg border border-[var(--border,#64748b)] p-4 m-0"
  disabled={busy || disabled}
>
  <legend class="font-semibold mb-4">CSV column mapping</legend>
  <p class={ui.hint}>
    Use a comma-separated CSV with a header row. Choose which columns contain
    names, numbers and notes. Mapping keeps one contact per source row.
  </p>
  {#if busy}<p class={ui.text} role="status">Processing CSV…</p>{/if}
  {#if error}<p class={ui.text} role="alert">{error}</p>{/if}
  {#if notice}<p class={ui.text} role="status">{notice}</p>{/if}
  {#if columns.length}
    <div
      class="fields mb-3 grid grid-cols-[repeat(auto-fit,minmax(min(12rem,100%),1fr))] items-end gap-3"
    >
      <label class="flex min-w-0 flex-col gap-[0.3rem] text-[0.9rem] text-label">
        Name or first name
        <Dropdown
          label="Name or first name"
          bind:value={mapping.Name}
          disabled={busy || disabled}
          onchange={invalidate}
          options={[
            { value: "", label: "No name column" },
            ...columns.map((column) => ({ value: column, label: column })),
          ]}
        />
      </label>
      <label class="flex min-w-0 flex-col gap-[0.3rem] text-[0.9rem] text-label">
        Last name to append
        <Dropdown
          label="Last name to append"
          bind:value={mapping.AdditionalName}
          disabled={busy || disabled}
          onchange={invalidate}
          options={[
            { value: "", label: "None" },
            ...columns.map((column) => ({ value: column, label: column })),
          ]}
        />
      </label>
      <label class="flex min-w-0 flex-col gap-[0.3rem] text-[0.9rem] text-label">
        Preferred primary number
        <Dropdown
          label="Preferred primary number"
          bind:value={mapping.Address}
          disabled={busy || disabled}
          onchange={invalidate}
          options={[
            { value: "", label: "Use the first additional number" },
            ...columns.map((column) => ({ value: column, label: column })),
          ]}
        />
      </label>
      <label class="flex min-w-0 flex-col gap-[0.3rem] text-[0.9rem] text-label">
        Notes
        <Dropdown
          label="Notes"
          bind:value={mapping.Notes}
          disabled={busy || disabled}
          onchange={invalidate}
          options={[
            { value: "", label: "None" },
            ...columns.map((column) => ({ value: column, label: column })),
          ]}
        />
      </label>
    </div>
    {#each mapping.Numbers as number, index}
      <div
        class="number mb-3 grid grid-cols-[repeat(auto-fit,minmax(min(12rem,100%),1fr))] items-end gap-3"
      >
        <label class="flex min-w-0 flex-col gap-[0.3rem] text-[0.9rem] text-label">
          Additional number {index + 1}
          <Dropdown
            label={`Additional number ${index + 1}`}
            bind:value={number.Column}
            disabled={busy || disabled}
            onchange={invalidate}
            options={[
              { value: "", label: "Choose a column" },
              ...columns.map((column) => ({ value: column, label: column })),
            ]}
          />
        </label>
        <label class="flex min-w-0 flex-col gap-[0.3rem] text-[0.9rem] text-label">
          Label
          <input
            class={ui.field}
            bind:value={number.Label}
            oninput={invalidate}
            maxlength="80"
            placeholder="Work, mobile, home…"
          />
        </label>
        <button class={ui.button} type="button" onclick={() => removeNumber(index)}>
          Remove number {index + 1}
        </button>
      </div>
    {/each}
    <p class={ui.hint}>
      The first nonempty number becomes primary. Empty and repeated numbers are
      skipped; rows with no number appear as errors in the preview. Labels apply
      to additional numbers. Missing names use the primary number.
    </p>
    <div class="actions flex flex-wrap gap-2">
      <button class={ui.button}
        type="button"
        disabled={mapping.Numbers.length >= 20}
        onclick={addNumber}>Add number column</button
      >
      <button class={ui.button}
        type="button"
        disabled={!mapping.Address &&
          !mapping.Numbers.some((number) => number.Column)}
        onclick={apply}>Apply mapping</button
      >
    </div>
  {/if}
</fieldset>
