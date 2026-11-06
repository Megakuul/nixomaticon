<script>
  import { tick } from "svelte";
  import { ui } from "../ui.js";
  import { Select } from "bits-ui";

  let {
    value = $bindable(),
    options = [],
    label,
    id,
    disabled = false,
    onchange,
  } = $props();
  let open = $state(false);
  let viewport = $state(null);
  let pressedOption = null;
  let keyboardHighlight = true;
  // Bits UI uses strings; retain the original boolean/number values in forms.
  const key = (value) => `${typeof value}:${String(value)}`;
  const selected = $derived(options.find((option) => option.value === (value ?? "")));
  const items = $derived(
    options.map((option) => ({ ...option, value: key(option.value) })),
  );

  function select(next) {
    const option = options.find((option) => key(option.value) === next);
    if (!option || disabled) return;
    value = option.value;
    onchange?.(value);
  }

  async function keepHighlightedVisible() {
    if (!keyboardHighlight) return;
    const currentViewport = viewport;
    await tick();
    await new Promise(requestAnimationFrame);
    if (!keyboardHighlight || !open || !currentViewport || viewport !== currentViewport) return;
    const item = currentViewport.querySelector("[data-highlighted]");
    if (!item) return;
    // Own scrolling here: Select.Viewport scrollIntoView also moves the page.
    const top = item.offsetTop;
    const bottom = top + item.offsetHeight;
    if (top < currentViewport.scrollTop) {
      currentViewport.scrollTop = top;
    } else if (bottom > currentViewport.scrollTop + currentViewport.clientHeight) {
      currentViewport.scrollTop = bottom - currentViewport.clientHeight;
    }
  }
  $effect(() => {
    if (disabled) open = false;
    if (!open) {
      pressedOption = null;
      keyboardHighlight = true;
    }
  });
</script>

<Select.Root
  type="single"
  value={key(value ?? "")}
  onValueChange={select}
  bind:open
  {disabled}
  {items}
  allowDeselect={false}
>
  <Select.Trigger {id} aria-label={label} class={[ui.control, `
    dropdown-trigger px-[0.8rem] py-[0.65rem] rounded-[6px] bg-field text-foreground min-h-[42px]
    shadow-control font-normal flex items-center justify-between gap-3 w-full min-w-0 text-left border
    border-field-border [&>svg]:shrink-0 [&>svg]:text-[#b8c3d0]
    [&>svg]:[transition:transform_140ms_ease] data-[state=open]:bg-[#303d49]
    data-[state=open]:border-[#8b9bad] [&[data-state='open']_>_svg]:[transform:rotate(180deg)]
    enabled:hover:bg-[#303d49] enabled:hover:border-[#8b9bad]
  `]}>
    <span class="overflow-hidden whitespace-nowrap text-ellipsis">{selected?.label || "Choose an option"}</span>
    <svg
      aria-hidden="true"
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"><path d="m6 9 6 6 6-6" /></svg
    >
  </Select.Trigger>
  <Select.Portal>
    <Select.Content
      class="
        p-[5px] z-[100] w-[var(--bits-select-anchor-width)] max-w-[calc(100vw_-_24px)] rounded-[6px]
        bg-[#303d49] text-foreground [box-shadow:0_10px_32px_#0009] outline-none
        border border-[#505b69]
      "
      sideOffset={6}
      align="start"
      strategy="fixed"
      preventScroll={false}
      onpointermovecapture={() => (keyboardHighlight = false)}
      onkeydowncapture={() => (keyboardHighlight = true)}
    >
      <div bind:this={viewport} role="presentation" class="
        relative min-h-0 flex-1
        max-h-[min(_18rem,_calc(var(--bits-select-content-available-height)_-_12px)_)] overflow-y-auto
        overscroll-contain [scrollbar-width:thin] [scrollbar-color:#505b69_#303d49]
      ">
        {#each options as option (key(option.value))}
          <Select.Item
            value={key(option.value)}
            label={option.label}
            disabled={option.disabled}
            onHighlight={keepHighlightedVisible}
            class="
              px-[0.7rem] py-[0.55rem] flex items-center justify-between gap-3 min-h-[38px] rounded-[5px]
              text-[0.9rem] leading-[1.45] cursor-pointer select-none outline-none wrap-anywhere
              data-selected:text-accent-text
              data-selected:bg-accent-surface data-highlighted:text-white data-highlighted:bg-accent-hover
              data-disabled:opacity-[0.4] data-disabled:cursor-not-allowed
            "
            onpointerdown={() => (pressedOption = key(option.value))}
            onpointerup={(event) => {
              // Opening the menu must not select an option under the release point.
              if (event.pointerType !== "touch" && pressedOption !== key(option.value))
                event.preventDefault();
              pressedOption = null;
            }}
          >
            {#snippet children({ selected })}
              <span>{option.label}</span>
              <span class="flex w-[16px] [flex:0_0_16px]" aria-hidden="true">
                {#if selected}<svg
                    width="16"
                    height="16"
                    viewBox="0 0 16 16"
                    fill="none"
                    stroke="currentColor"
                    stroke-width="2"
                    stroke-linecap="round"
                    stroke-linejoin="round"><path d="m3 8 3 3 7-7" /></svg
                  >{/if}
              </span>
            {/snippet}
          </Select.Item>
        {:else}
          <div class="p-3 text-muted">No options available</div>
        {/each}
      </div>
    </Select.Content>
  </Select.Portal>
</Select.Root>
