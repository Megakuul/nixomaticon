"use strict";

const $ = (id) => document.getElementById(id);
const backend = () => window.go.main.App;

const form = $("form");
const fields = $("fields");
const action = $("action");
const prompt = $("prompt");
const labels = {
  idle: "Disconnected",
  authorizing: "Waiting for authorization…",
  connecting: "Connecting…",
  reconnecting: "Reconnecting…",
  connected: "Connected",
  disconnecting: "Disconnecting…",
  error: "Connection failed",
};

let state = "idle";
let connectedAt = 0;
let sawState = false;
let logCount = 0;
let retryAt = 0;

function setState(next, error) {
  if (next === "disconnected") next = "idle";
  state = next;
  document.body.dataset.state = next;
  $("status").textContent = labels[next] ?? next;
  $("error").hidden = !error;
  $("error").textContent = error ?? "";

  const busy = ["authorizing", "connecting", "reconnecting", "disconnecting"].includes(next);
  const live = busy || next === "connected";
  fields.disabled = live;
  action.disabled = next === "disconnecting" || next === "authorizing";
  action.textContent = !live || next === "authorizing" ? "Connect" : next === "connected" ? "Disconnect" : "Cancel";
  $("info").hidden = next !== "connected";

  if (next === "connected") connectedAt = Date.now();
  if (next !== "connected" && prompt.open) prompt.close();
}

function log(level, msg, time) {
  const li = document.createElement("li");
  li.className = level;
  const t = document.createElement("time");
  t.textContent = time ?? new Date().toTimeString().slice(0, 8);
  li.append(t, msg);
  const list = $("log");
  list.append(li);
  while (list.children.length > 500) list.firstChild.remove();
  list.scrollTop = list.scrollHeight;
  if (level !== "debug") $("logcount").textContent = ++logCount;
}

function onEvent(e) {
  switch (e.event) {
    case "log":
      log(e.level, e.msg, e.time);
      if (e.level === "error") $("logbox").open = true;
      break;
    case "state":
      sawState = true;
      retryAt = e.state === "reconnecting" ? Date.now() + (e.retry ?? 0) * 1000 : 0;
      setState(e.state, e.error);
      tick();
      break;
    case "connected":
      $("device").textContent = e.device || "–";
      $("gateway").textContent = e.gateway || "–";
      $("routes").textContent = (e.routes ?? []).length;
      break;
    case "prompt":
      $("promptmsg").textContent = e.msg || "Enter your one-time code.";
      $("promptvalue").value = "";
      prompt.showModal();
      $("promptvalue").focus();
      break;
    case "exit":
      if (!sawState && (e.code === 126 || e.code === 127)) setState("idle", "Authorization cancelled.");
      else if (state !== "error") setState("idle", e.code ? `Helper exited with code ${e.code}.` : undefined);
      break;
  }
}

form.addEventListener("submit", async (ev) => {
  ev.preventDefault();
  if (["connected", "connecting", "reconnecting"].includes(state)) {
    backend().Disconnect().catch((err) => log("error", String(err)));
    return;
  }
  const data = Object.fromEntries(new FormData(form));
  const cfg = { ...data, port: Number(data.port) || 443, autoReconnect: form.elements.autoReconnect.checked };
  sawState = false;
  setState("authorizing");
  try {
    await backend().Connect(cfg);
    form.elements.password.value = "";
  } catch (err) {
    setState("error", String(err));
  }
});

$("promptform").addEventListener("submit", () => {
  backend().Respond($("promptvalue").value);
});
$("promptcancel").addEventListener("click", () => {
  prompt.close();
  backend().Disconnect();
});
$("verbose").addEventListener("change", (ev) => $("logbox").classList.toggle("verbose", ev.target.checked));

function tick() {
  if (state === "reconnecting" && retryAt) {
    const left = Math.max(0, Math.ceil((retryAt - Date.now()) / 1000));
    $("status").textContent = left ? `Reconnecting in ${left}s…` : "Reconnecting…";
  }
  if (state !== "connected") return;
  const s = Math.floor((Date.now() - connectedAt) / 1000);
  const pad = (n) => String(n).padStart(2, "0");
  $("uptime").textContent = `${pad(Math.floor(s / 3600))}:${pad(Math.floor(s / 60) % 60)}:${pad(s % 60)}`;
}
setInterval(tick, 1000);

window.addEventListener("DOMContentLoaded", async () => {
  window.runtime.EventsOn("vb:event", onEvent);
  const cfg = await backend().LoadConfig();
  for (const [k, v] of Object.entries(cfg)) {
    const el = form.elements[k];
    if (!el || v === undefined || v === null) continue;
    if (el.type === "checkbox") el.checked = Boolean(v);
    else el.value = v;
  }
  (cfg.server ? form.elements.password : form.elements.server).focus();
});
