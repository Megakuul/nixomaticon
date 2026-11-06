<script>
  import Icon from "./Icon.svelte";
  import logo from "../assets/images/icon.svg";

  let { page, unreadMessages = 0, callCount = 0, navigate } = $props();
  const pages = [
    ["overview", "Overview"], ["phone", "Phone"], ["contacts", "Contacts"],
    ["history", "Recent calls"], ["messages", "Messages"],
    ["accounts", "Accounts"], ["settings", "Audio & settings"],
  ];
</script>

<aside class="
  px-3 py-5 sticky top-0 h-[calc(100vh_-_44px)] overflow-y-auto border-r border-r-border
  bg-surface-inset [@media(max-height:_550px)]:pt-0 max-[1100px]:px-[0.4rem] max-[1100px]:py-4
">
  <a class="flex items-center px-2 pb-6 outline-offset-3 max-[1100px]:hidden"
    href="#overview" onclick={() => navigate("overview")}>
    <img src={logo} alt="Voiper" class="w-[112px] h-[60px] object-contain [@media(max-height:_550px)]:h-[42px]" />
  </a>
  <nav aria-label="Main navigation" class="flex flex-col gap-[0.35rem] max-[700px]:gap-0">
    {#each pages as [key, label]}
      {@const count = key === "messages" ? unreadMessages : key === "phone" ? callCount : 0}
      <button
        aria-label={label}
        title={label}
        aria-current={page === key ? "page" : undefined}
        class={[
          `relative flex items-center gap-[0.85rem] w-full min-h-[44px] px-[0.8rem] py-[0.7rem]
          rounded-[6px] text-left font-medium outline-offset-3 cursor-pointer transition-colors
          duration-100 max-[1100px]:p-[0.65rem] max-[1100px]:justify-center
          max-[700px]:text-[0.85rem] hover:bg-[#36424e]/60 hover:text-foreground focus-visible:bg-[#36424e]/60 focus-visible:text-foreground
          after:absolute after:left-0 after:top-3 after:bottom-3 after:w-[2px] after:rounded-full
          after:transition-colors after:duration-100`,
          page === key ? "text-accent-text after:bg-brand-border" : "text-muted after:bg-transparent",
        ]}
        onclick={() => navigate(key)}>
        <Icon name={key} />
        <span class="max-[1100px]:hidden">{label}</span>
        {#if count}
          <span class="
            ml-auto px-[0.7rem] py-[0.3rem] rounded-[4px] bg-[#35414c] text-xs
            max-[1100px]:absolute max-[1100px]:top-0 max-[1100px]:right-0
            max-[1100px]:px-1 max-[1100px]:py-[0.1rem] max-[1100px]:text-[0.6rem]
          ">{count}</span>
        {/if}
      </button>
    {/each}
  </nav>
</aside>
