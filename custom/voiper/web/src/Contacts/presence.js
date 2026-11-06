// Contact classification and status presentation shared by contact lists.

// Short numbers without a trunk prefix are extensions on the account's own PBX.
export function isExtension(address) {
  return /^[1-9]\d{1,5}$/.test((address || "").trim());
}

// The extension is the address that carries PBX status, even when the
// contact's primary number is a mobile phone.
export function extensionOf(contact) {
  if (isExtension(contact.Address)) return contact.Address.trim();
  return (contact.Numbers || []).map((n) => n.Address.trim()).find(isExtension) || "";
}

function looksMobile(address) {
  // Leading zeros cover both the outside-line prefix and international dialing.
  const digits = address.replace(/[^\d]/g, "").replace(/^0+/, "");
  return /^(41)?7[5-9]\d{7}$/.test(digits) || /^(49)?1[5-7]\d{8,9}$/.test(digits);
}

// Lists the primary address first, followed by additional numbers.
export function contactNumbers(contact) {
  const numbers = [{ Label: "", Address: contact.Address }, ...(contact.Numbers || [])];
  const seen = new Set();
  return numbers
    .filter((n) => n.Address && !seen.has(n.Address) && seen.add(n.Address))
    .map((n) => {
      const kind = isExtension(n.Address)
        ? "internal"
        : /cell|mobil|handy|natel/i.test(n.Label) || looksMobile(n.Address)
          ? "mobile"
          : "external";
      return { ...n, kind };
    });
}

export const statusStyles = {
  available: { label: "Available", dot: "bg-[#4fd18b]", text: "text-[#7fe0ad]" },
  away: { label: "Away", dot: "bg-[#f2b950]", text: "text-[#f5cb7d]" },
  "in-call": { label: "On a call", dot: "bg-[#f2845c]", text: "text-[#f6a385]" },
  ringing: { label: "Ringing", dot: "bg-[#6fb3f2]", text: "text-[#97c8f6]" },
  dnd: { label: "Do not disturb", dot: "bg-[#ec5b6e]", text: "text-[#f3909e]" },
  offline: { label: "Logged off", dot: "bg-[#6b7682]", text: "text-muted" },
  subscribing: { label: "Checking status…", dot: "bg-transparent border border-[#7c8793]", text: "text-muted" },
  unavailable: { label: "Status unavailable", dot: "bg-transparent border border-[#7c8793]", text: "text-muted" },
  unknown: { label: "Status unknown", dot: "bg-transparent border border-[#7c8793]", text: "text-muted" },
};

export function statusStyle(state) {
  return statusStyles[state] || statusStyles.unknown;
}

export function initials(name) {
  // Directory names often end in a short code such as "(MaAb)".
  const words = (name || "").replace(/\(.*?\)/g, "").trim().split(/\s+/).filter(Boolean);
  if (!words.length) return "#";
  return (words[0][0] + (words.length > 1 ? words[words.length - 1][0] : "")).toUpperCase();
}
