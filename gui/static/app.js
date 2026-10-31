// Front Desk page. Talks only to frontdesk.py, on the same address.
// Everything shown is built with textContent, never as HTML.
"use strict";

const $ = (sel) => document.querySelector(sel);
const TABS = ["cards", "new", "tally"];
const FIELDS = ["seat", "models", "budget", "refill", "duration"];
let csrf = null;
let state = null;
let stateTag = "";      // ETag of the state on screen
let checkedAt = 0;
let live = "off";       // off, on, lost
let lostAt = 0;
let stream = null;
let pollTimer = null;
let loading = false;
let again = false;
let tallyView = "today";
let tallyDrawn = "";
let confirming = null;  // id of the card asking "are you sure?"
let modelsShown = "";
let needLogin = false;
let inDocker = false;   // Front Desk runs in Docker (gui/README.md)
const drawn = new Map();      // card id -> [signature, element]
const cancelled = new Map();  // card id -> note left in its place
const pending = new Map();    // card id -> {busy} or {err} of a cancel

function h(tag, props, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (v == null || v === false) continue;
    if (k === "class") el.className = v;
    else if (k === "text") el.textContent = v;
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else el.setAttribute(k, v === true ? "" : v);
  }
  for (const kid of kids.flat()) {
    if (kid == null || kid === false) continue;
    el.append(kid instanceof Node ? kid : String(kid));
  }
  return el;
}

// --- numbers and times, in plain words -------------------------------

// Made once: toLocaleString makes a new formatter on every call,
// which is most of the time spent drawing a card.
const NUM = new Intl.NumberFormat();
const CENTS = new Intl.NumberFormat(undefined,
  { minimumFractionDigits: 2, maximumFractionDigits: 2 });
const TINY = new Intl.NumberFormat(undefined,
  { minimumFractionDigits: 2, maximumFractionDigits: 4 });
const ONE = new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 });

// A refill can leave a spend like -1e-18: that is $0, with no sign.
const zero = (x) => x <= 0 && x > -5e-7 ? 0 : x;

// Cents, and more digits for tiny budgets like a game seat's $0.015.
function money(x) {
  if (x == null) return "—";
  x = zero(x);
  if (x > 0 && x < 0.0001) return "under $" + TINY.format(0.0001);
  return "$" + (x < 0.1 ? TINY : CENTS).format(x);
}

// Tiles are narrow: no cents from $100 up, and short words.
function moneyShort(x) {
  if (x == null) return "—";
  x = zero(x);
  if (x > 0 && x < 0.01) return "<$" + CENTS.format(0.01);
  if (x >= 99.995) return "$" + NUM.format(Math.round(x));
  return "$" + CENTS.format(x);
}

function count(x) {
  if (x == null) return "—";
  const k = Math.round(x / 1e3);
  if (x >= 1e6 || k >= 1000) return ONE.format(x / 1e6) + "M";
  if (x >= 1e4) return k + "K";
  return NUM.format(x);
}

// "30d" -> "30 days", as mint-key.sh's --duration takes it.
const UNITS = { s: "second", m: "minute", h: "hour", d: "day",
                w: "week", mo: "month" };

function lasts(d) {
  const m = /^(\d+)(mo|[smhdw])$/.exec(d || "");
  if (!m) return d;
  return plural(+m[1], UNITS[m[2]]);
}

// A refill period: "1d" -> "every day", "2m" -> "every 2 minutes".
function every(d) {
  const m = /^(\d+)(mo|[smhdw])$/.exec(d || "");
  if (!m) return "every " + d;
  return "every " + (m[1] === "1" ? UNITS[m[2]] : lasts(d));
}

function plural(n, word) {
  return NUM.format(n) + " " + word + (n === 1 ? "" : "s");
}

function ago(ms) {
  const s = Math.round(ms / 1000);
  if (s < 5) return "just now";
  if (s < 60) return s + " s ago";
  const m = Math.round(s / 60);
  if (m < 60) return plural(m, "minute") + " ago";
  const hr = Math.round(m / 60);
  if (hr < 48) return plural(hr, "hour") + " ago";
  return plural(Math.round(hr / 24), "day") + " ago";
}

// One formatter for every card: making one per call is the slow part.
const DAY = new Intl.DateTimeFormat(undefined,
  { day: "numeric", month: "short", year: "numeric" });

const CLOCK = new Intl.DateTimeFormat(undefined,
  { hour: "numeric", minute: "2-digit" });
const WHEN = new Intl.DateTimeFormat(undefined, { weekday: "short",
  day: "numeric", month: "short", hour: "numeric", minute: "2-digit" });

// How long until a time ahead: "12 minutes", "3 hours", "5 days".
function span(ms) {
  const min = ms / 6e4;
  return min < 90 ? plural(Math.max(1, Math.round(min)), "minute")
    : min < 2880 ? plural(Math.round(min / 60), "hour")
    : plural(Math.round(min / 1440), "day");
}

function until(iso) {
  const ms = Date.parse(iso) - Date.now();
  const day = DAY.format(new Date(iso));
  if (ms <= 0) return "Expired " + ago(-ms) + " (" + day + ")";
  return "Expires in " + span(ms) + " (" + day + ")";
}

// The next refill, in local time: "at 1:00 AM (in 3 hours)". Past
// it, the proxy's reset job is about to run.
function refillAt(iso) {
  const ms = Date.parse(iso) - Date.now();
  if (!(ms > 0)) return "any moment now";
  const at = (ms < 864e5 ? CLOCK : WHEN).format(new Date(iso));
  return "at " + at + " (" + (ms < 6e4 ? "in under a minute"
    : "in " + span(ms)) + ")";
}

// --- talking to frontdesk.py -----------------------------------------

async function api(path, body, retried) {
  const opts = { credentials: "same-origin", cache: "no-store" };
  if (body !== undefined) {
    opts.method = "POST";
    opts.headers = { "Content-Type": "application/json",
                     "X-CSRF-Token": csrf || "" };
    opts.body = JSON.stringify(body);
  }
  let r;
  try {
    r = await fetch(path, opts);
  } catch (e) {
    throw new Error("Front Desk is not answering. Is it still running?");
  }
  let data = {};
  try { data = await r.json(); } catch (e) { /* not JSON */ }
  if (r.status === 401 && path !== "/api/login") showLogin();
  if (r.status === 403 && body !== undefined && !retried &&
      await refreshSession()) {
    // Front Desk restarted: new token, then the same request once.
    return api(path, body, true);
  }
  if (!r.ok) {
    const err = new Error(data.error || "Error " + r.status);
    err.field = data.field;
    throw err;
  }
  return data;
}

// A fresh token and base URL. True if the token changed.
async function refreshSession() {
  try {
    const s = await api("/api/session");
    const changed = !!s.csrf && s.csrf !== csrf;
    if (s.csrf) csrf = s.csrf;
    showSeatUrl(s.seat_url);
    return changed;
  } catch (e) {
    return false;
  }
}

async function boot() {
  window.addEventListener("hashchange", showTab);
  $("#login-form").addEventListener("submit", login);
  $("#mint-form").addEventListener("submit", mint);
  $("#mint-form").addEventListener("input", clearFieldError);
  $("#mint-form").addEventListener("change", clearFieldError);
  $("#logout").addEventListener("click", logout);
  $("#copy-seat-url").addEventListener("click", (e) =>
    copy($("#seat-url"), e.currentTarget, "address"));
  for (const b of document.querySelectorAll(".seg button")) {
    b.addEventListener("click", () => {
      tallyView = b.dataset.view;
      renderTally();
    });
  }
  setupDialog();
  setInterval(renderSub, 1000);
  setInterval(tickCards, 1000);
  let s;
  try {
    s = await api("/api/session");
  } catch (e) {
    return setPill("down", "No answer", e.message);
  }
  $("#plain-http").hidden = !s.lan;
  needLogin = s.need_login;
  if (s.docker) dockerHints();
  if (!s.csrf) return showLogin();
  csrf = s.csrf;
  showSeatUrl(s.seat_url);
  startLive();
}

// In Docker, the log and the settings live elsewhere: say where.
function dockerHints() {
  inDocker = true;
  const code = (text) => h("code", { text });
  $("#pw-hint").replaceChildren("It is in Front Desk's log (",
    code("docker compose logs frontdesk"), "), or set as ",
    code("FRONTDESK_PASSWORD"), " in seats.env.");
  $("#seat-url-hint").replaceChildren("A guess, from the address you " +
    "opened this page by. If seats use another, set ",
    code("FRONTDESK_SEAT_URL"), " in .env.");
}

function showLogin() {
  stopLive();
  csrf = null;
  for (const t of document.querySelectorAll(".tab")) t.hidden = true;
  $("#tabs").hidden = true;
  $("#logout").hidden = true;
  $("#notes").replaceChildren();
  $("#login").hidden = false;
  setPill("wait", "Locked", "Log in to see the desk");
  $("#pw").focus();
}

async function login(ev) {
  ev.preventDefault();
  const msg = $("#login-msg");
  msg.textContent = "";
  try {
    const r = await api("/api/login", { password: $("#pw").value });
    csrf = r.csrf;
    showSeatUrl(r.seat_url);
    $("#pw").value = "";
    $("#login").hidden = true;
    startLive();
  } catch (e) {
    msg.textContent = e.message;
  }
}

async function logout() {
  try { await api("/api/logout", {}); } catch (e) { /* gone anyway */ }
  showLogin();
}

// --- live updates ------------------------------------------------------
// The stream only says "changed"; the state itself comes from
// /api/state, gzipped and answered 304 when nothing is new. Polling is
// the fallback for browsers without EventSource.

function startLive() {
  $("#login").hidden = true;
  $("#tabs").hidden = false;
  $("#logout").hidden = !needLogin;
  showTab();
  stopLive();
  if (!window.EventSource) return poll();
  load();  // don't wait for the stream's first word
  stream = new EventSource("/api/events");
  stream.addEventListener("changed", () => {
    setLive("on");
    load();
  });
  stream.addEventListener("tick", (e) => {
    checkedAt = Date.now() - JSON.parse(e.data).age * 1000;
    setLive("on");
  });
  stream.onopen = () => {
    // Back after a gap: Front Desk may have restarted, new token.
    if (live === "lost") refreshSession();
  };
  stream.onerror = () => {
    setLive("lost");
    if (stream && stream.readyState === EventSource.CLOSED) {
      // Refused (logged out, or too many pages): ask why, then retry.
      stream = null;
      setTimeout(recheck, 3000);
    }
  };
}

function stopLive() {
  if (stream) stream.close();
  stream = null;
  clearTimeout(pollTimer);
}

async function recheck() {
  try {
    const s = await api("/api/session");
    if (!s.csrf) return showLogin();
    csrf = s.csrf;
    showSeatUrl(s.seat_url);
    startLive();
  } catch (e) {
    setTimeout(recheck, 5000);
  }
}

// Fetch the shared state; true if Front Desk answered.
async function load() {
  if (loading) {
    again = true;
    return true;
  }
  loading = true;
  try {
    do {
      again = false;
      const r = await fetch("/api/state", { credentials: "same-origin",
                                            cache: "no-cache" });
      if (r.status === 401) {
        showLogin();
        return false;
      }
      if (!r.ok) throw new Error("Error " + r.status);
      checkedAt = Date.now();
      const tag = r.headers.get("ETag") || "";
      if (!state || tag !== stateTag) {
        const s = await r.json();
        stateTag = tag;
        render(s);
      }
    } while (again);
    return true;
  } catch (e) {
    if (!stream) setLive("lost");  // a stream reports its own loss
    return false;
  } finally {
    loading = false;
  }
}

async function poll() {
  const ok = await load();
  if (!csrf) return;  // logged out
  setLive(ok ? "on" : "lost");
  pollTimer = setTimeout(poll, 3000);
}

function setLive(v) {
  if (v === "lost" && live !== "lost") lostAt = Date.now();
  live = v;
  renderSub();
}

// --- screens ----------------------------------------------------------

function showTab() {
  let tab = location.hash.slice(1);
  if (!TABS.includes(tab)) tab = "cards";
  if (!$("#login").hidden) return;
  for (const t of TABS) $("#tab-" + t).hidden = t !== tab;
  if (tab === "tally") renderTally();
  for (const a of document.querySelectorAll("#tabs a")) {
    a.setAttribute("aria-current",
                   a.dataset.tab === tab ? "page" : "false");
  }
}

function setPill(kind, text, sub) {
  const pill = $("#pill");
  pill.className = "pill " + kind;
  // It is a live region: only touch it when the words change.
  if (pill.textContent !== text) pill.textContent = text;
  if (sub != null) $("#sub").textContent = sub;
}

function renderSub() {
  if (!state || !csrf) return;
  const d = state.desk;
  // Front Desk itself gone for a few seconds: say so at the top.
  const away = live === "lost" && Date.now() - lostAt > 3000;
  if (away) setPill("wait", "Reconnecting");
  else if (d.up) setPill("ok", "Desk open");
  else setPill("down", "Not answering");
  const bits = [];
  if (d.litellm) bits.push("LiteLLM " + d.litellm);
  bits.push(plural(state.cards.length, "key card"));
  // The stream sends a tick every 15 s even when nothing changed.
  const quiet = Date.now() - checkedAt;
  if (away) bits.push("Front Desk is not answering, trying again");
  else if (live === "lost") bits.push("reconnecting…");
  else if (stream && quiet < 20000) bits.push("updating live");
  else bits.push("checked " + ago(quiet));
  $("#sub").textContent = bits.join(" · ");
}

function render(s) {
  state = s;
  const d = s.desk;
  renderSub();
  let notes = d.notes.map((n) => h("p", { class: "note bad", text: n }));
  if (!d.up) {
    // One box: what it means, what to do, then the details.
    notes = [h("div", { class: "note bad" },
      h("p", {}, h("strong", { text: "The proxy is not answering. " }),
        "Key cards below are the last ones it showed. On the proxy " +
        "machine, run docker compose ps, then see the README's " +
        "Troubleshooting: the proxy."),
      d.notes.map((n) => h("p", { class: "small", text: n })))];
  }
  $("#notes").replaceChildren(...notes);
  renderCards();
  renderModels();
  if (!$("#tab-tally").hidden) renderTally();
}

let emptyCard = null;
let cardsMinute = 0;     // the minute the card times were last set in
let nextDue = Infinity;  // the next refill time on any card, in ms

// Times on cards are in words, like "in 3 minutes", so they change
// even when nothing else does. Each sits in its own span.
const TIMES = {
  ago: (iso) => ago(Date.now() - Date.parse(iso)),
  until,
  refill: refillAt,
  // When a card that waits for its refill works again. The proxy
  // hands refills out on a timer, not on the dot (gui/README.md).
  wait: (iso) => Date.parse(iso) > Date.now()
    ? refillAt(iso) + ", or soon after" : "any moment now",
};

function timeWords(kind, iso) {
  return h("span", { "data-time": kind, "data-at": iso,
                     text: TIMES[kind](iso) });
}

// Each minute, and as a refill falls due, set only those words
// again: the cards stay as they are, with any focus, selection or
// message in them.
function tickCards() {
  if (!state || !csrf) return;
  const now = Date.now();
  if (now < nextDue && Math.floor(now / 6e4) === cardsMinute) return;
  cardsMinute = Math.floor(now / 6e4);
  nextDue = Infinity;
  for (const c of state.cards) {
    const due = Date.parse(c.resets);
    if (due > now) nextDue = Math.min(nextDue, due);
  }
  for (const el of $("#cards-list").querySelectorAll("[data-time]")) {
    const text = TIMES[el.dataset.time](el.dataset.at);
    if (el.textContent !== text) el.textContent = text;
  }
}

function renderCards() {
  const cards = state.cards;
  const working = cards.filter((c) => c.state === "ok").length;
  const today = state.tally ? state.tally.seats.reduce(
    (a, r) => a + (r.t_spend || 0), 0) : null;
  $("#cards-tiles").replaceChildren(
    tile("Key cards", cards.length),
    tile("Working", working),
    tile("Spent today", moneyShort(today)));
  // Rebuild only the cards that changed.
  const now = Date.now();
  const els = cards.filter((c) => !cancelled.has(c.id)).map((c) => {
    const due = Date.parse(c.resets);
    if (due > now) nextDue = Math.min(nextDue, due);
    const sig = JSON.stringify([c, confirming === c.id,
                                pending.get(c.id) || null]);
    const old = drawn.get(c.id);
    if (old && old[0] === sig) return old[1];
    const el = cardView(c);
    el.dataset.key = c.id;
    drawn.set(c.id, [sig, el]);
    return el;
  });
  // A cancelled card leaves a note in its place for a few seconds.
  const gone = [...cancelled.values()].sort((a, b) => a.index - b.index);
  for (const g of gone) els.splice(Math.min(g.index, els.length), 0, g.el);
  if (!els.length) {
    emptyCard = emptyCard || h("div", { class: "card", "data-key": "-" },
      h("p", { text: "No key cards yet." }),
      h("a", { href: "#new", class: "button primary",
               text: "Make the first one" }));
    els.push(emptyCard);
  }
  // A card drawn again keeps the keyboard focus on the same button.
  const f = document.activeElement;
  const fCard = f && f.tagName === "BUTTON" ? f.closest(".card") : null;
  // Cards that stay are never moved, so focus and selected text in
  // them survive a card coming or going.
  const list = $("#cards-list");
  const want = new Set(els);
  for (const k of [...list.children]) if (!want.has(k)) k.remove();
  els.forEach((el, i) => {
    const at = list.children[i];
    if (at !== el) list.insertBefore(el, at || null);
  });
  if (fCard && document.activeElement !== f) {
    const d = drawn.get(fCard.dataset.key);
    const same = d && [...d[1].querySelectorAll("button")].find(
      (b) => b.className === f.className);
    if (same) same.focus({ preventScroll: true });
  }
  const ids = new Set(cards.map((c) => c.id));
  for (const id of drawn.keys()) if (!ids.has(id)) drawn.delete(id);
  // Cards kept from before may have been drawn in an earlier minute.
  tickCards();
}

function tile(label, value) {
  return h("div", { class: "tile" },
    h("div", { class: "label", text: label }),
    h("div", { class: "value",
               text: typeof value === "number" ? NUM.format(value)
                 : value }));
}

const STATES = {
  ok: ["✓", "Working"],
  spent: ["!", "Budget used up"],
  waiting: ["↻", "Waiting for refill"],
  expired: ["○", "Expired"],
  blocked: ["✕", "Blocked"],
};

// Seats the office game made. Front Desk asks the game which ones.
const isGame = (c) => c.game === true;
const RESET = "python3 game/office.py reset";

// What to do about a card that stopped working.
const HINTS = {
  expired: "An expired card still holds its seat name. Cancel it to " +
    "free the name.",
  spent: "This budget never refills: cancel the card and make a new " +
    "one to top up.",
  blocked: "Someone blocked it in the proxy's own admin tools. Cancel " +
    "it to free the name.",
};

function cardView(c) {
  const name = c.seat || "(no seat name)";
  const [icon, word] = STATES[c.state] || ["?", c.state];
  const meter = h("div", { class: "meter", role: "meter",
    "aria-valuemin": "0", "aria-valuemax": String(c.budget || 0),
    "aria-valuenow": String(c.spent),
    "aria-label": "Spent of budget" }, h("span"));
  let share = 0;
  if (c.budget) share = Math.min(1, c.spent / c.budget);
  else if (c.budget === 0) share = 1;
  meter.firstChild.style.width = (share * 100).toFixed(1) + "%";
  if (share >= 1) meter.classList.add("full");
  else if (share >= 0.8) meter.classList.add("high");
  // A budget that refills counts only since the last refill.
  const refills = c.refills && c.budget != null;
  const spent = c.budget == null ? "Spent " + money(c.spent) +
    ", no budget limit" : "Spent " + money(c.spent) + " of " +
    money(c.budget) + (refills ? " since the last refill" : "");
  if (refills) {
    meter.setAttribute("aria-label", "Spent since the last refill");
  }
  const small = [];
  small.push(c.expires ? timeWords("until", c.expires) : "Never expires");
  if (c.ends) small.push("key ends in …" + c.ends);
  if (c.used) small.push(["last used ", timeWords("ago", c.used)]);
  const el = h("article", { class: "card" + (c.state === "ok" ? ""
                                              : " dim") },
    h("div", { class: "card-top" },
      h("h3", { text: name }),
      h("span", { class: "badge " + c.state },
        h("span", { class: "icon", "aria-hidden": "true", text: icon }),
        word)),
    isGame(c) && h("p", { class: "kind", text: "Office game seat" }),
    h("p", { text: "Models: " + (c.models.join(", ") || "all") }),
    meter,
    h("p", { text: spent }),
    // Waiting: the hint below says when, so it is said once.
    refills && h("p", { class: "small" }, "Refills " + every(c.refills),
      c.resets && c.state !== "waiting" &&
        [", next ", timeWords("refill", c.resets)]),
    h("p", { class: "small" }, dots(small)));
  if (c.state === "waiting") {
    el.append(h("p", { class: "small" }, "It refills on its own ",
      c.resets ? timeWords("wait", c.resets) : "soon",
      ". Nothing to do: no need to cancel it."));
  } else if (HINTS[c.state]) {
    el.append(h("p", { class: "small", text: HINTS[c.state] }));
  }
  if (isGame(c)) {
    el.append(h("p", { class: "small" }, "The office game made it. " +
      "To remove all game seats, run ", h("code", { text: RESET }), "."));
  }
  if (confirming === c.id) el.append(confirmView(c, name));
  else {
    // Its cancel may still be running, or have failed, while another
    // card asks "are you sure?". say() has told screen readers.
    const p = pending.get(c.id) || {};
    if (p.err) el.append(h("p", { class: "msg", text: p.err }));
    el.append(h("div", { class: "actions" },
      h("button", { type: "button", class: "danger", disabled: p.busy,
        onclick: () => askCancel(c.id),
        text: p.busy ? "Cancelling…" : "Cancel card" })));
  }
  return el;
}

// Short facts joined by dots; each one stays on a single line.
function dots(bits) {
  return bits.flatMap((b, i) => [i ? " · " : null,
    h("span", { class: "nw" }, b)]);
}

function askCancel(id) {
  confirming = id;
  if (!(pending.get(id) || {}).busy) pending.delete(id);
  renderCards();
  // The safe answer gets the focus, not the destructive one.
  const keep = drawn.get(id)[1].querySelector(".keep");
  if (keep) keep.focus({ preventScroll: true });
}

// The cancel's progress is kept in pending, so a live update that
// redraws the card while it waits still shows it.
function confirmView(c, name) {
  const p = pending.get(c.id) || {};
  // No role=alert: cancelCard has said the error once, and the box
  // may be drawn again while it is open.
  const msg = h("p", { class: "msg", text: p.err });
  const yes = h("button", { type: "button", class: "danger solid",
    disabled: p.busy, text: p.busy ? "Cancelling…" : "Yes, cancel it",
    onclick: () => cancelCard(c, name, yes) });
  return h("div", { class: "confirm", role: "group",
                    "aria-label": "Confirm cancel" },
    h("p", { text: "Cancel the key card for " + name + "? Apps using " +
      "it stop working at once. This can't be undone." }),
    isGame(c) && h("p", {}, "The game still counts on this seat. To " +
      "end the game, run ", h("code", { text: RESET }), " instead."),
    h("div", { class: "actions" }, yes,
      h("button", { type: "button", class: "keep", text: "Keep it",
        onclick: () => {
          confirming = null;
          if (!(pending.get(c.id) || {}).busy) pending.delete(c.id);
          renderCards();
          // Back on the button that asked, not lost on the page.
          const d = drawn.get(c.id);
          const b = d && d[1].querySelector("button.danger");
          if (b) b.focus({ preventScroll: true });
        } })),
    msg);
}

async function cancelCard(c, name, yes) {
  // Where the card is now, in case it has left the list by the end.
  const card = yes.closest(".card");
  const was = { index: [...$("#cards-list").children].indexOf(card),
                height: card.offsetHeight };
  pending.set(c.id, { busy: true });
  yes.disabled = true;
  yes.textContent = "Cancelling…";
  yes.closest(".confirm").querySelector(".msg").textContent = "";
  try {
    await api("/api/revoke", { seat: c.seat, id: c.id });
    pending.delete(c.id);
    leaveNote(c.id, "Cancelled the key card for " + name + ".", was);
  } catch (e) {
    // Drawn again from pending: the card on screen now may be a new
    // element, not the one that was clicked.
    pending.set(c.id, { err: e.message });
    say(e.message);
    if (state) renderCards();
  }
}

// Swap a cancelled card for a note of the same height, so the message
// is where the thumb is and nothing below jumps up under it.
function leaveNote(id, text, was) {
  const list = $("#cards-list");
  const note = h("div", { class: "card gone", "data-key": "gone-" + id },
    h("p", { text }));
  // The card as drawn now: a live update may have redrawn it, or
  // taken it off the list already.
  const d = drawn.get(id);
  const card = d && d[1].parentNode === list ? d[1] : null;
  const height = card ? card.offsetHeight : was.height;
  if (height) note.style.minHeight = height + "px";
  cancelled.set(id, { el: note, index: Math.max(0, card
    ? [...list.children].indexOf(card) : was.index) });
  // Another card may be asking "are you sure?" by now: leave it open,
  // and leave the page where it is.
  if (confirming === id) confirming = null;
  say(text);
  renderCards();
  if (!confirming) note.firstChild.scrollIntoView({ block: "nearest" });
  setTimeout(() => {
    cancelled.delete(id);
    if (state) renderCards();
  }, 6000);
}

// For screen readers: the visible note is not announced by itself.
function say(text) {
  const f = $("#flash");
  f.textContent = text;
  clearTimeout(say.timer);
  say.timer = setTimeout(() => { f.textContent = ""; }, 6000);
}

// --- making a key card ------------------------------------------------

function renderModels() {
  const key = JSON.stringify([state.models, state.defaults]);
  if (key === modelsShown) return;  // keep what the user ticked
  modelsShown = key;
  const want = new Set(state.defaults.models);
  const box = $("#models");
  if (!state.models.length) {
    box.replaceChildren(h("p", { class: "empty",
      text: "No models yet: the proxy is not answering." }));
  } else {
    box.replaceChildren(...state.models.map((m) =>
      h("label", { class: "check" },
        h("input", { type: "checkbox", value: m,
                     checked: want.has(m) }),
        h("span", { class: "mono", text: m }))));
  }
  const budget = $("#budget");
  if (!budget.value) budget.value = state.defaults.budget;
  const sel = $("#duration");
  const dur = state.defaults.duration;
  if (![...sel.options].some((o) => o.value === dur)) {
    sel.append(h("option", { value: dur, text: lasts(dur) }));
  }
  sel.value = dur;
}

function clearErrors() {
  for (const f of FIELDS) {
    $("#" + f + "-msg").textContent = "";
    const input = $("#" + f);
    if (input) input.removeAttribute("aria-invalid");
  }
  $("#mint-msg").textContent = "";
}

function clearFieldError(ev) {
  const field = ev.target.closest(".field");
  const msg = field && field.querySelector(".msg");
  if (msg) msg.textContent = "";
  ev.target.removeAttribute("aria-invalid");
}

// Say what is wrong right under the field, with the field in view.
function fieldError(f, text) {
  const msg = $("#" + f + "-msg");
  msg.textContent = text;
  msg.closest(".field").scrollIntoView({ block: "nearest" });
  const input = f === "models" ? $("#models input") : $("#" + f);
  if (!input) return;
  if (f !== "models") input.setAttribute("aria-invalid", "true");
  input.focus({ preventScroll: true });
}

async function mint(ev) {
  ev.preventDefault();
  const btn = $("#mint-btn");
  clearErrors();
  const seat = $("#seat").value.trim();
  const models = [...document.querySelectorAll("#models input:checked")]
    .map((i) => i.value);
  // No shell here, so a typed $ is harmless: drop it. The page writes
  // 2,50 in some languages, so take a decimal comma too.
  const budget = $("#budget").value.trim().replace(/^\$/, "")
    .replace(/^(\d{1,6}),(\d{1,2})$/, "$1.$2");
  const refill = $("#refill").value;
  const duration = $("#duration").value;
  if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,39}$/.test(seat)) {
    return fieldError("seat", "Seat names use letters, digits, dots, " +
      "dashes and underscores, up to 40, with no spaces.");
  }
  if (!models.length) {
    return fieldError("models", "Pick at least one model.");
  }
  if (!/^[0-9]{1,6}([.][0-9]{1,2})?$/.test(budget) || !(+budget > 0)) {
    return fieldError("budget", "Budget is a number of US dollars " +
      "above 0, like 5 or 2.50.");
  }
  btn.disabled = true;
  btn.textContent = "Making the card…";
  try {
    const r = await api("/api/mint", { seat, models, budget, refill,
                                       duration });
    $("#seat").value = "";
    showKey(r);
  } catch (e) {
    if (FIELDS.includes(e.field)) fieldError(e.field, e.message);
    else {
      $("#mint-msg").textContent = e.message;
      $("#mint-msg").scrollIntoView({ block: "nearest" });
    }
  } finally {
    btn.disabled = false;
    btn.textContent = "Make key card";
  }
}

// --- the new key, shown once ------------------------------------------

function setupDialog() {
  const d = $("#newkey");
  $("#nk-copy").addEventListener("click", (e) =>
    copy($("#nk-key"), e.currentTarget, "key"));
  $("#nk-copy-url").addEventListener("click", (e) =>
    copy($("#nk-url"), e.currentTarget, "address"));
  $("#nk-save").addEventListener("click", saveKey);
  $("#nk-done").addEventListener("click", () => closeKey());
  // closeKey has wiped it already; this is for other ways out. A
  // late event must not wipe a key shown since.
  d.addEventListener("close", () => { if (!d.open) wipeKey(); });
  d.addEventListener("cancel", (e) => {
    // Esc: make sure it was saved first.
    if (!confirm("Close? The key is not shown again.")) e.preventDefault();
  });
}

function showKey(r) {
  const d = $("#newkey");
  $("#nk-title").textContent = "Key card for " + r.seat;
  $("#nk-key").value = r.key;
  $("#nk-url").value = r.base_url;
  showSeatUrl(r.base_url);
  $("#nk-save").dataset.seat = r.seat;
  $("#nk-model").textContent = r.models.join(" or ");
  $("#nk-models").textContent =
    (r.models.length > 1 ? "Any one of these. " : "") + "Budget " +
    money(+r.budget) + (r.refill ? ", refills " + every(r.refill) : "") +
    " · lasts " + lasts(r.duration) + ".";
  if (d.showModal) d.showModal();
  else d.setAttribute("open", "");
  $("#nk-copy").focus();
}

function closeKey() {
  const d = $("#newkey");
  wipeKey();  // now, not when the close event gets round to it
  if (d.close) d.close();
  else d.removeAttribute("open");
  location.hash = "#cards";
}

function wipeKey() {
  $("#nk-key").value = "";
  $("#nk-url").value = "";
  for (const b of document.querySelectorAll("#newkey button")) {
    clearTimeout(b.timer);
    if (b.dataset.label) b.textContent = b.dataset.label;
  }
}

function showSeatUrl(url) {
  if (!url) return;
  $("#seat-url").value = url;
  $("#connect").hidden = false;
}

// Copy a field's text. Says "Copied" only when it really was.
async function copy(field, btn, what) {
  const text = field.value;
  if (!btn.dataset.label) btn.dataset.label = btn.textContent;
  let ok = false;
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      ok = true;
    }
  } catch (e) { /* fall back below */ }
  if (!ok) ok = copyBySelection(text, btn);
  if (!ok) {
    field.focus();
    field.select();
  }
  btn.textContent = ok ? "Copied" : "Press and hold the " + what +
    " to copy";
  clearTimeout(btn.timer);
  btn.timer = setTimeout(() => { btn.textContent = btn.dataset.label; },
                         ok ? 2500 : 8000);
}

// Plain http pages may not use the clipboard API, so copy a selection
// instead. The box goes inside the open dialog: a modal dialog makes
// the rest of the page inert, and nothing there can be selected.
function copyBySelection(text, btn) {
  const ta = h("textarea", { class: "offscreen", readonly: true,
                             "aria-hidden": "true", tabindex: "-1" });
  ta.value = text;
  (btn.closest("dialog") || document.body).append(ta);
  ta.select();
  ta.setSelectionRange(0, text.length);
  let ok = false;
  try {
    ok = document.execCommand("copy") &&
      ta.selectionEnd - ta.selectionStart === text.length;
  } catch (e) {
    ok = false;
  }
  ta.remove();
  btn.focus();
  return ok;
}

function saveKey(e) {
  const seat = e.currentTarget.dataset.seat || "seat";
  const blob = new Blob([$("#nk-key").value + "\n"],
                        { type: "text/plain" });
  const a = h("a", { href: URL.createObjectURL(blob),
                     download: seat + ".key" });
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(a.href), 1000);
}

// --- the tally --------------------------------------------------------

function renderTally() {
  if (!state) return;
  const t = state.tally;
  const refills = state.cards.some((c) => c.refills);
  // Drawn only while its tab is open, and only when it changed.
  const sig = JSON.stringify([t, tallyView, refills]);
  if (sig === tallyDrawn) return;
  tallyDrawn = sig;
  for (const b of document.querySelectorAll(".seg button")) {
    b.setAttribute("aria-pressed", String(b.dataset.view === tallyView));
  }
  if (!t) {
    $("#tally-tiles").replaceChildren();
    $("#tally-seats").replaceChildren(h("p", { class: "empty",
      text: "No tally yet." }));
    $("#tally-models").replaceChildren();
    $("#tally-note").textContent = "";
    return;
  }
  const today = tallyView === "today";
  const pick = (r) => today
    ? { spend: r.t_spend, reqs: r.t_reqs, tokens: r.t_tokens,
        fails: r.t_fails }
    : { spend: r.spend, reqs: r.reqs, tokens: r.tokens, fails: r.fails };
  const sum = (rows, f) => rows.some((r) => pick(r)[f] == null) ? null
    : rows.reduce((a, r) => a + pick(r)[f], 0);
  // Seats and models add up the same use. In the proxy's own numbers,
  // all time per seat also has each live card's spend: the larger.
  const total = (f) => {
    const m = sum(t.models, f), s = sum(t.seats, f);
    return m == null ? s : s == null ? m : Math.max(m, s);
  };
  $("#tally-tiles").replaceChildren(
    tile("Spent", moneyShort(total("spend"))),
    tile("Requests", count(total("reqs"))),
    tile("Tokens", count(total("tokens"))));
  // Models on your own Ollama cost $0: then bars show tokens.
  const all = [...t.seats, ...t.models].map(pick);
  const by = all.some((v) => v.spend > 0) ? "spend"
    : all.some((v) => v.tokens > 0) ? "tokens" : "reqs";
  // A blank name is a request refused before a key or model matched.
  $("#tally-seats").replaceChildren(...bars(t.seats, pick, by, (r) =>
    !r.name ? "(refused: no valid key)"
      : r.name + (r.live ? "" : " (cancelled)")));
  $("#tally-models").replaceChildren(...bars(t.models, pick, by, (r) =>
    r.name || "(refused before a model was picked)"));
  const midnight = new Date(t.day + "T00:00:00Z").toLocaleTimeString(
    undefined, { hour: "numeric", minute: "2-digit" });
  const own = t.source === "proxy";
  let note = today ? "Today runs from midnight UTC, which is " +
    midnight + " your time." : own ? "All time: what the proxy " +
    "still knows." : "All time: everything the proxy has saved, " +
    "cancelled cards included.";
  if (by === "tokens") {
    note += " Bars show tokens, since everything used here was free.";
  }
  if (own) {
    note += " Showing the proxy's own numbers: full history needs " +
      (inDocker ? "Front Desk to reach the database, at DATABASE_URL " +
        "in seats.env." : "Front Desk to run as a user who can run " +
        "docker compose.");
    // The proxy keeps each live card's spend, only since its last
    // refill if it refills; everything else only for today.
    if (!today) {
      note += " Requests and tokens here are recent only, as is the " +
        "spend of models" + (refills ? ", cancelled cards and cards " +
        "that refill." : " and cancelled cards.");
    }
  }
  $("#tally-note").textContent = note;
}

function bars(rows, pick, by, label) {
  const shown = rows.map((r) => [r, pick(r)])
    .filter(([, v]) => v.spend || v.reqs)
    .sort((a, b) => (b[1][by] || 0) - (a[1][by] || 0) ||
                    (b[1].reqs || 0) - (a[1].reqs || 0));
  if (!shown.length) {
    return [h("p", { class: "empty", text: tallyView === "today"
      ? "Nothing used yet today." : "Nothing used yet." })];
  }
  const top = Math.max(...shown.map(([, v]) => v[by] || 0));
  return shown.map(([r, v]) => {
    const fill = h("span", { class: "fill" });
    const x = v[by] || 0;
    fill.style.width = (x > 0 && top > 0 ? Math.max(x / top * 100, 1)
                        : 0) + "%";
    // Every request refused: nothing was really used.
    const refused = v.reqs > 0 && v.fails >= v.reqs;
    const value = by === "spend" ? money(v.spend)
      : by === "tokens" ? count(v.tokens) + " tokens"
      : plural(v.reqs, "request");
    const bits = [];
    if (refused) bits.push(plural(v.fails, "refused request"));
    else {
      if (v.reqs != null) bits.push(plural(v.reqs, "request"));
      if (by !== "tokens" && v.tokens != null) {
        bits.push(count(v.tokens) + " tokens");
      }
      if (by !== "spend" && v.spend != null) bits.push(money(v.spend));
      if (v.fails) bits.push(NUM.format(v.fails) + " refused");
    }
    const name = label(r) + (refused && r.name ? " · refused" : "");
    return h("div", { class: "bar-row" },
      h("div", { class: "top-line" },
        h("span", { text: name }),
        h("span", { class: "val", text: value })),
      h("div", { class: "track", role: "img",
                 "aria-label": name + ": " + value }, fill),
      h("p", { class: "small", text: bits.join(" · ") }));
  });
}

boot();
