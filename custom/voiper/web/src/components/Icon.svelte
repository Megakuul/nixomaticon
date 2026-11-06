<script>
  let { name, size = 20 } = $props();
  const paths = {
    overview: "M3 3h7v7H3z M14 3h7v7h-7z M3 14h7v7H3z M14 14h7v7h-7z",
    phone: "M7 3H4a1 1 0 0 0-1 1c0 9.4 7.6 17 17 17a1 1 0 0 0 1-1v-3l-5-2-2 2a15 15 0 0 1-7-7l2-2z",
    contacts: "M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2 M9 3a4 4 0 1 0 0 8 4 4 0 0 0 0-8 M18 8h4 M18 12h4 M19 16h3",
    history: "M3 11a9 9 0 1 1 2 7 M3 4v7h7 M12 7v5l3 2",
    messages: "M21 11a8 8 0 0 1-8 8H7l-4 3V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z M7 8h10 M7 12h7",
    accounts: "M4 3h16v18H4z M8 7h8 M8 11h8 M8 15h4 M16 17h.01",
    settings: "M4 6h16 M4 12h16 M4 18h16 M8 3v6 M16 9v6 M10 15v6",
    arrow: "M5 12h14 M14 7l5 5-5 5",
    incoming: "M18 6 6 18 M6 7v11h11",
    outgoing: "M6 18 18 6 M7 6h11v11",
    microphone: "M9 5a3 3 0 0 1 6 0v7a3 3 0 0 1-6 0z M5 11v1a7 7 0 0 0 14 0v-1 M12 19v3 M8 22h8",
    speaker: "M11 4 6 8H2v8h4l5 4z M15 8a6 6 0 0 1 0 8 M18 5a10 10 0 0 1 0 14",
    mobile: "M7 2h10a1 1 0 0 1 1 1v18a1 1 0 0 1-1 1H7a1 1 0 0 1-1-1V3a1 1 0 0 1 1-1z M11 18h2",
    building: "M4 21V5a1 1 0 0 1 1-1h9a1 1 0 0 1 1 1v16 M15 9h4a1 1 0 0 1 1 1v11 M2 21h20 M8 8h3 M8 12h3 M8 16h3",
    edit: "M4 20h4L19 9l-4-4L4 16z M13 7l4 4",
    trash: "M4 7h16 M9 7V4h6v3 M6 7l1 13h10l1-13 M10 11v6 M14 11v6",
    search: "M11 4a7 7 0 1 0 0 14 7 7 0 0 0 0-14z M20 20l-4-4",
    star: "M12 3l2.8 5.7 6.2.9-4.5 4.4 1 6.2L12 17.3 6.5 20.2l1-6.2L3 9.6l6.2-.9z",
    close: "M6 6l12 12 M18 6 6 18",
    transfer: "M4 8h13 M13 4l4 4-4 4 M20 16H7 M11 12l-4 4 4 4",
    pause: "M8 5v14 M16 5v14",
    "microphone-off": "M9 9V5a3 3 0 0 1 5.8-1 M15 9v3a3 3 0 0 1-4.6 2.5 M5 11v1a7 7 0 0 0 11.6 5.3 M19 11v1c0 .7-.1 1.4-.3 2 M12 19v3 M8 22h8 M3 3l18 18",
    "user-plus": "M15 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2 M8.5 3a4 4 0 1 0 0 8 4 4 0 0 0 0-8 M19 8v6 M16 11h6",
    conference: "M12 3a3.5 3.5 0 1 0 0 7 3.5 3.5 0 0 0 0-7 M5 21v-1.5A4.5 4.5 0 0 1 9.5 15h5a4.5 4.5 0 0 1 4.5 4.5V21 M4.5 7.5a2.5 2.5 0 0 0 0 5 M19.5 7.5a2.5 2.5 0 0 1 0 5 M1 19v-1a3 3 0 0 1 2-2.8 M23 19v-1a3 3 0 0 0-2-2.8",
    send: "M4 12 20 4l-6 16-3-7z M11 13l9-9",
    dialpad: "M5 4a1 1 0 1 0 2 0a1 1 0 1 0-2 0 M11 4a1 1 0 1 0 2 0a1 1 0 1 0-2 0 M17 4a1 1 0 1 0 2 0a1 1 0 1 0-2 0 M5 10a1 1 0 1 0 2 0a1 1 0 1 0-2 0 M11 10a1 1 0 1 0 2 0a1 1 0 1 0-2 0 M17 10a1 1 0 1 0 2 0a1 1 0 1 0-2 0 M5 16a1 1 0 1 0 2 0a1 1 0 1 0-2 0 M11 16a1 1 0 1 0 2 0a1 1 0 1 0-2 0 M17 16a1 1 0 1 0 2 0a1 1 0 1 0-2 0 M11 21a1 1 0 1 0 2 0a1 1 0 1 0-2 0",
  };
</script>

<svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" class="shrink-0 align-middle"><path d={paths[name] || paths.phone} /></svg>
