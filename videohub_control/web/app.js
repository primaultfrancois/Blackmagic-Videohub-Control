"use strict";

// --- state ---------------------------------------------------------------
let S = null;                 // last snapshot from the server
const ui = {
  tab: "panel",
  kind: "video",              // "video" | "monitor"
  selected: new Set(),        // selected destination indexes
  anchor: null,               // for shift-click ranges
  armed: null,                // armed source in take mode
  take: pref("take", false),
  filter: "",
  size: pref("size", 112),
  labelsSig: "",              // detects label table rebuilds
  hover: null,                // matrix hover {r, c}
};

const $ = (sel) => document.querySelector(sel);
const esc = (s) => String(s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

function pref(key, fallback) {
  try { const v = localStorage.getItem("vh." + key); return v === null ? fallback : JSON.parse(v); }
  catch { return fallback; }
}
function setPref(key, value) {
  try { localStorage.setItem("vh." + key, JSON.stringify(value)); } catch { /* private mode */ }
}

function toast(msg, isError = false) {
  const el = document.createElement("div");
  el.className = "toast" + (isError ? " error" : "");
  el.innerHTML = ic(isError ? "alert" : "check") + `<span>${esc(msg)}</span>`;
  $("#toasts").append(el);
  setTimeout(() => { el.classList.add("out"); setTimeout(() => el.remove(), 300); }, isError ? 5000 : 2500);
}

async function api(path, body = {}) {
  try {
    const res = await fetch("/api/" + path, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error || res.statusText);
    return data;
  } catch (err) {
    toast(err.message || String(err), true);
    throw err;
  }
}

// --- helpers on the hub model -----------------------------------------------
const hub = () => (S && S.connection && S.connection.hub) || null;
function outputs(h = hub()) {
  if (!h) return { labels: [], routing: [], locks: [] };
  return ui.kind === "monitor"
    ? { labels: h.monitorLabels, routing: h.monitorRouting, locks: h.monitorLocks }
    : { labels: h.outputLabels, routing: h.routing, locks: h.locks };
}
const pad = (n) => String(n).padStart(2, "0");
const ic = (name, cls = "ic") => `<svg class="${cls}"><use href="#i-${name}"/></svg>`;

// --- server events ---------------------------------------------------------
function listen() {
  const es = new EventSource("/api/events");
  es.onmessage = (e) => { S = JSON.parse(e.data); render(); };
  es.onerror = () => {
    setStatus("disconnected", "Serveur local injoignable…");
  };
}

// --- rendering -------------------------------------------------------------
function render() {
  const c = S.connection;
  const h = hub();
  setStatus(c ? c.status : "disconnected", c ? (c.status === "connected" ? `Connecté à ${c.host}` : c.error || "Connexion…") : "Déconnecté");
  $("#connect-btn").hidden = !!c;
  $("#disconnect-btn").hidden = !c;
  if (c && document.activeElement !== $("#host-input")) $("#host-input").value = c.port === 9990 ? c.host : `${c.host}:${c.port}`;

  $("#device-summary").innerHTML = h
    ? `<div class="dev-card"><span class="dev-icon">${ic("router")}</span><div class="dev-text">
        <div class="dev-name">${esc(h.friendlyName || h.modelName)}</div><div class="dev-model">${esc(h.modelName)}</div></div>
        <span class="badge">${h.videoInputs} × ${h.videoOutputs}</span>` +
      (h.monitoringOutputs ? `<span class="badge">+${h.monitoringOutputs} mon.</span>` : "") + `</div>`
    : "";

  if (h && h.devicePresent && h.devicePresent !== "true") {
    $("#empty-title").textContent = "Videohub indisponible";
    $("#empty-text").textContent = h.devicePresent === "needs_update"
      ? "Le Videohub demande une mise à jour du firmware (Blackmagic Videohub Setup)."
      : "Le serveur Videohub ne détecte pas de routeur.";
  } else if (c && !h) {
    $("#empty-title").textContent = "Connexion en cours…";
    $("#empty-text").textContent = c.error || `Connexion à ${c.host}:${c.port}`;
  } else {
    $("#empty-title").textContent = "Aucun Videohub connecté";
    $("#empty-text").textContent = "Saisissez l'adresse IP de votre Videohub en haut à droite, ou choisissez-en un dans la liste.";
  }
  const ready = !!h && (!h.devicePresent || h.devicePresent === "true");
  $("#empty-state").hidden = ready;
  $("#workspace").hidden = !ready;

  renderSidebar();
  if (!ready) { ui.selected.clear(); ui.armed = null; return; }

  $("#kind-switch").hidden = !h.monitoringOutputs;
  if (!h.monitoringOutputs && ui.kind === "monitor") ui.kind = "video";
  const size = outputs().labels.length;
  for (const o of [...ui.selected]) if (o >= size) ui.selected.delete(o);
  if (ui.armed !== null && ui.armed >= h.videoInputs) ui.armed = null;

  if (ui.tab === "panel") renderPanel();
  if (ui.tab === "matrix") drawMatrix();
  if (ui.tab === "labels") renderLabels();
  if (ui.tab === "info") renderInfo();
}

function setStatus(status, text) {
  $("#status-dot").className = "status-dot " + status;
  $("#conn-field").title = text;
  $("#status-text").textContent = { connected: "En ligne", connecting: "Connexion" }[status] || "Hors ligne";
}

function renderSidebar() {
  const c = S.connection;
  const hubList = $("#hub-list");
  hubList.innerHTML = S.hubs.length ? "" : `<li class="empty-row">Aucun Videohub enregistré</li>`;
  for (const hb of S.hubs) {
    const li = document.createElement("li");
    if (c && c.host === hb.host && c.port === hb.port) li.className = "active";
    li.innerHTML = `<span class="li-icon">${ic("router")}</span><span class="name">${esc(hb.name)}<span class="sub mono">${esc(hb.host)}${hb.port !== 9990 ? ":" + hb.port : ""}</span></span>
      <button class="icon-btn" title="Supprimer">${ic("x")}</button>`;
    li.onclick = () => api("connect", { host: hb.host, port: hb.port });
    li.querySelector("button").onclick = (e) => {
      e.stopPropagation();
      if (confirm(`Supprimer « ${hb.name} » de la liste ?`)) api("hubs/delete", hb);
    };
    hubList.append(li);
  }

  const disc = $("#discovered-list");
  disc.innerHTML = S.discovered.length ? `<li class="found-label">Trouvés sur le réseau</li>` : "";
  for (const d of S.discovered) {
    const li = document.createElement("li");
    if (c && c.host === d.host) li.className = "active";
    li.innerHTML = `<span class="li-icon">${ic("radar")}</span><span class="name">${esc(d.name)}<span class="sub mono">${esc(d.host)}</span></span>`;
    li.title = d.model || d.name;
    li.onclick = () => api("connect", { host: d.host, port: 9990 });
    disc.append(li);
  }
  $("#save-hub-btn").disabled = !c;

  const salvos = $("#salvo-list");
  const h = hub();
  salvos.innerHTML = "";
  $("#save-salvo-btn").disabled = !h;
  $("#salvo-hint").textContent = !h ? "Connectez un Videohub pour gérer ses salves."
    : S.salvos.length ? "" : "Une salve mémorise le routage pour le rappeler en un clic.";
  for (const s of S.salvos) {
    const n = s.video.length + s.monitor.length;
    const li = document.createElement("li");
    li.className = "salvo";
    li.title = "Rappeler cette salve";
    li.innerHTML = `<span class="li-icon">${ic("play")}</span><span class="name">${esc(s.name)}<span class="sub">${n} destination${n > 1 ? "s" : ""}</span></span>
      <button class="icon-btn" title="Supprimer">${ic("x")}</button>`;
    li.onclick = async () => {
      const r = await api("salvos/recall", { name: s.name });
      toast(`Salve « ${s.name} » rappelée` + (r.skipped ? ` (${r.skipped} destination(s) verrouillée(s) ignorée(s))` : ""));
    };
    li.querySelector("button").onclick = (e) => {
      e.stopPropagation();
      if (confirm(`Supprimer la salve « ${s.name} » ?`)) api("salvos/delete", { name: s.name });
    };
    salvos.append(li);
  }
}

// --- panel -------------------------------------------------------------------
function matches(label, idx) {
  if (!ui.filter) return true;
  const f = ui.filter.toLowerCase();
  return label.toLowerCase().includes(f) || String(idx + 1) === f;
}

function renderPanel() {
  const h = hub();
  const out = outputs();
  const dest = $("#dest-grid");
  const src = $("#src-grid");
  const frag = document.createDocumentFragment();

  out.labels.forEach((label, o) => {
    if (!matches(label, o) && !ui.selected.has(o)) return;
    const lock = out.locks[o];
    const b = document.createElement("button");
    b.className = "xbtn" + (ui.selected.has(o) ? " selected" : "") + (lock === "L" ? " locked-other" : "");
    b.dataset.out = o;
    const from = h.inputLabels[out.routing[o]] ?? "—";
    b.title = `${o + 1} · ${label}\nSource : ${out.routing[o] + 1} · ${from}` +
      (lock === "O" ? "\nVerrouillée par vous" : lock === "L" ? "\nVerrouillée par un autre utilisateur" : "");
    b.innerHTML = `<span class="num">${pad(o + 1)}</span><span class="label">${esc(label)}</span>
      <span class="from">${ic("arrow")}<span>${esc(from)}</span></span>` +
      (lock === "O" ? ic("lock", "lock own") : lock === "L" ? ic("lock", "lock other") : "");
    frag.append(b);
  });
  dest.replaceChildren(frag);
  $("#dest-count").textContent = out.labels.length;
  $("#src-count").textContent = h.inputLabels.length;

  // Which sources feed the selection?
  const feeding = new Map();
  for (const o of ui.selected) feeding.set(out.routing[o], (feeding.get(out.routing[o]) || 0) + 1);
  const all = ui.selected.size;

  const frag2 = document.createDocumentFragment();
  h.inputLabels.forEach((label, i) => {
    if (!matches(label, i) && ui.armed !== i && !feeding.has(i)) return;
    const b = document.createElement("button");
    let cls = "xbtn";
    if (ui.armed === i) cls += " armed";
    else if (feeding.has(i)) cls += feeding.get(i) === all ? " current" : " partial";
    b.className = cls;
    b.dataset.in = i;
    b.title = `${i + 1} · ${label}`;
    b.innerHTML = `<span class="num">${pad(i + 1)}</span><span class="label">${esc(label)}</span>`;
    frag2.append(b);
  });
  src.replaceChildren(frag2);

  renderActionbar();
}

function renderActionbar() {
  const h = hub();
  const out = outputs();
  const sel = [...ui.selected].sort((a, b) => a - b);
  const info = $("#selection-info");
  const chip = (cls, n, t) => `<span class="chip ${cls}">${n !== null ? `<span class="n">${pad(n + 1)}</span>` : ""}<span class="t">${esc(t)}</span></span>`;
  const arrow = ic("arrow", "ic arrow");
  if (!sel.length) {
    info.innerHTML = `<span class="placeholder">Sélectionnez une destination, puis une source</span>`;
  } else {
    let html = sel.length === 1 ? chip("dst", sel[0], out.labels[sel[0]]) : chip("dst", null, `${sel.length} destinations`);
    if (ui.armed !== null) html += arrow + chip("arm", ui.armed, h.inputLabels[ui.armed]);
    else if (sel.length === 1) html += arrow + chip("src", out.routing[sel[0]], h.inputLabels[out.routing[sel[0]]] ?? "");
    info.innerHTML = html;
  }

  const lockBtn = $("#lock-btn");
  lockBtn.disabled = !sel.length;
  const states = sel.map((o) => out.locks[o]);
  const [action, text, icon] = states.some((s) => s === "L") ? ["F", "Forcer le déverrouillage", "unlock"]
    : states.length && states.every((s) => s === "O") ? ["U", "Déverrouiller", "unlock"] : ["O", "Verrouiller", "lock"];
  lockBtn.dataset.action = action;
  lockBtn.innerHTML = ic(icon) + `<span>${text}</span>`;
  lockBtn.classList.toggle("danger", lockBtn.dataset.action === "F");

  const take = $("#take-btn");
  take.hidden = !ui.take;
  take.disabled = ui.armed === null || !sel.length;
}

function selectDest(o, e) {
  if (e.shiftKey && ui.anchor !== null) {
    const [a, b] = [Math.min(ui.anchor, o), Math.max(ui.anchor, o)];
    const visible = [...document.querySelectorAll("#dest-grid .xbtn")].map((x) => +x.dataset.out);
    if (!(e.ctrlKey || e.metaKey)) ui.selected.clear();
    for (const v of visible) if (v >= a && v <= b) ui.selected.add(v);
  } else if (e.ctrlKey || e.metaKey) {
    ui.selected.has(o) ? ui.selected.delete(o) : ui.selected.add(o);
    ui.anchor = o;
  } else {
    ui.selected = new Set([o]);
    ui.anchor = o;
  }
  ui.armed = null;
  renderPanel();
}

async function routeSelection(input) {
  const out = outputs();
  const targets = [...ui.selected];
  const free = targets.filter((o) => out.locks[o] !== "L");
  if (free.length < targets.length) toast(`${targets.length - free.length} destination(s) verrouillée(s) par un autre utilisateur ignorée(s)`, true);
  const changes = free.filter((o) => out.routing[o] !== input).map((o) => [o, input]);
  ui.armed = null;
  if (changes.length) await api("route", { kind: ui.kind, routes: changes }).catch(() => {});
  render();
}

function clickSource(i) {
  if (!ui.selected.size) { toast("Sélectionnez d'abord une destination"); return; }
  if (ui.take) {
    ui.armed = ui.armed === i ? null : i;
    renderPanel();
  } else {
    routeSelection(i);
  }
}

// --- matrix ----------------------------------------------------------------
// Grid and dots are drawn on a canvas; headers are real HTML text so they
// stay sharp at any zoom / display scaling.
const M = { cell: 24, rowHead: 190, colHead: 150, sig: "" };
const MX_FONT = "500 12px Inter, 'SF Pro Text', 'Segoe UI Variable', 'Segoe UI', system-ui, sans-serif";

function matrixColors() {
  const cs = getComputedStyle(document.documentElement);
  const v = (n) => cs.getPropertyValue(n).trim();
  return { bg: v("--panel"), alt: v("--panel-2"), line: v("--line"), accent: v("--accent"),
           soft: v("--accent-soft"), other: v("--lock-other") };
}

function textWidth(texts) {
  const ctx = (textWidth.c ||= document.createElement("canvas").getContext("2d"));
  ctx.font = MX_FONT;
  return texts.reduce((w, t) => Math.max(w, ctx.measureText(t).width), 0);
}

function buildMatrixHeaders(h, out) {
  const sig = [ui.kind, h.inputLabels.join("\u0001"), out.labels.join("\u0001"), out.locks.join("")].join("|");
  if (sig === M.sig) return;
  M.sig = sig;
  const clamp = (v, a, b) => Math.max(a, Math.min(b, Math.ceil(v)));
  M.colHead = clamp(textWidth(h.inputLabels.map((l, i) => `${i + 1}  ${l}`)) + 26, 90, 240);
  M.rowHead = clamp(textWidth(out.labels.map((l, i) => `${i + 1}  ${l}`)) + 44, 150, 300);
  const wrap = $("#matrix-wrap");
  wrap.style.setProperty("--mx-row", M.rowHead + "px");
  wrap.style.setProperty("--mx-col", M.colHead + "px");
  wrap.style.setProperty("--mx-cell", M.cell + "px");

  $("#mx-cols-inner").innerHTML = h.inputLabels.map((l, c) =>
    `<div class="mx-col" style="left:${c * M.cell}px"><span class="n">${c + 1}</span>${esc(l)}</div>`).join("");
  $("#mx-rows-inner").innerHTML = out.labels.map((l, r) => {
    const lock = out.locks[r] === "L" ? " lock-other" : out.locks[r] === "O" ? " lock-own" : "";
    return `<div class="mx-row${lock}" style="top:${r * M.cell}px"><span class="n">${r + 1}</span><span class="t">${esc(l)}</span></div>`;
  }).join("");
  M.hoverEls = [];
}

function drawMatrix() {
  const h = hub();
  if (!h) return;
  const out = outputs();
  buildMatrixHeaders(h, out);
  const rows = out.labels.length, cols = h.inputLabels.length;
  const scroller = $("#matrix-scroll");
  const canvas = $("#matrix-canvas");
  const gw = cols * M.cell, gh = rows * M.cell;
  $("#matrix-spacer").style.width = M.rowHead + gw + "px";
  $("#matrix-spacer").style.height = M.colHead + gh + "px";

  const sx = scroller.scrollLeft, sy = scroller.scrollTop;
  $("#mx-cols-inner").style.transform = `translateX(${-sx}px)`;
  $("#mx-rows-inner").style.transform = `translateY(${-sy}px)`;

  // Canvas covers only the visible grid area, with an integer backing store.
  const vw = Math.max(0, Math.min(scroller.clientWidth - M.rowHead, gw + 1));
  const vh = Math.max(0, Math.min(scroller.clientHeight - M.colHead, gh + 1));
  const dpr = window.devicePixelRatio || 1;
  const bw = Math.round(vw * dpr), bh = Math.round(vh * dpr);
  if (canvas.width !== bw || canvas.height !== bh) {
    canvas.width = bw; canvas.height = bh;
    canvas.style.width = vw + "px"; canvas.style.height = vh + "px";
  }
  const ctx = canvas.getContext("2d");
  const k = vw ? bw / vw : dpr;
  ctx.setTransform(k, 0, 0, k, 0, 0);
  const snap = (v) => (Math.round(v * k) + 0.5) / k;   // crisp 1-device-pixel lines
  const C = matrixColors();
  const c0 = Math.max(0, Math.floor(sx / M.cell)), c1 = Math.min(cols, Math.ceil((sx + vw) / M.cell));
  const r0 = Math.max(0, Math.floor(sy / M.cell)), r1 = Math.min(rows, Math.ceil((sy + vh) / M.cell));
  const hv = ui.hover;

  ctx.fillStyle = C.bg; ctx.fillRect(0, 0, vw, vh);
  for (let r = r0; r < r1; r++) {
    const y = r * M.cell - sy;
    if (r % 2) { ctx.fillStyle = C.alt; ctx.fillRect(0, y, gw - sx, M.cell); }
    if (out.locks[r] === "L") { ctx.fillStyle = "rgba(255,93,93,.10)"; ctx.fillRect(0, y, gw - sx, M.cell); }
  }
  if (hv) {
    ctx.fillStyle = C.soft;
    ctx.fillRect(0, hv.r * M.cell - sy, gw - sx, M.cell);
    ctx.fillRect(hv.c * M.cell - sx, 0, M.cell, gh - sy);
  }
  ctx.strokeStyle = C.line; ctx.lineWidth = 1 / k; ctx.beginPath();
  for (let c = c0; c <= c1; c++) { const x = snap(c * M.cell - sx); ctx.moveTo(x, 0); ctx.lineTo(x, gh - sy); }
  for (let r = r0; r <= r1; r++) { const y = snap(r * M.cell - sy); ctx.moveTo(0, y); ctx.lineTo(gw - sx, y); }
  ctx.stroke();
  for (let r = r0; r < r1; r++) {
    const c = out.routing[r];
    if (c < c0 || c >= c1) continue;
    const color = out.locks[r] === "L" ? C.other : C.accent;
    ctx.save();
    ctx.fillStyle = color; ctx.shadowColor = color; ctx.shadowBlur = 8;
    ctx.beginPath();
    ctx.arc(c * M.cell - sx + M.cell / 2, r * M.cell - sy + M.cell / 2, M.cell * .25, 0, Math.PI * 2);
    ctx.fill();
    ctx.restore();
  }

  // Header hover highlight
  for (const el of M.hoverEls || []) el.classList.remove("hover");
  M.hoverEls = [];
  if (hv) {
    const col = $("#mx-cols-inner").children[hv.c], row = $("#mx-rows-inner").children[hv.r];
    for (const el of [col, row]) if (el) { el.classList.add("hover"); M.hoverEls.push(el); }
  }
}

function matrixHit(e) {
  const h = hub();
  const scroller = $("#matrix-scroll");
  const rect = scroller.getBoundingClientRect();
  const x = e.clientX - rect.left, y = e.clientY - rect.top;
  if (x < M.rowHead || y < M.colHead) return null;
  const c = Math.floor((x - M.rowHead + scroller.scrollLeft) / M.cell);
  const r = Math.floor((y - M.colHead + scroller.scrollTop) / M.cell);
  if (c < 0 || r < 0 || c >= h.inputLabels.length || r >= outputs().labels.length) return null;
  return { r, c };
}

function matrixFoot() {
  const h = hub(), out = outputs(), hv = ui.hover, foot = $("#matrix-foot");
  if (!hv) { foot.textContent = "Survolez un point de croisement ; cliquez pour router."; return; }
  const cur = out.routing[hv.r];
  const chip = (cls, n, t) => `<span class="chip ${cls}"><span class="n">${pad(n + 1)}</span><span class="t">${esc(t)}</span></span>`;
  foot.innerHTML = chip("dst", hv.r, out.labels[hv.r]) + ic("arrow", "ic arrow") + chip("src", hv.c, h.inputLabels[hv.c]) +
    (cur === hv.c ? `<span class="note">routage actuel</span>` : `<span class="note">actuellement : ${esc(h.inputLabels[cur] ?? "")}</span>`) +
    (out.locks[hv.r] === "L" ? `<span class="warn">verrouillée</span>` : "");
}

// --- labels ------------------------------------------------------------------
const LABEL_KINDS = [
  ["input", "Entrées", (h) => h.inputLabels],
  ["output", "Sorties", (h) => h.outputLabels],
  ["monitor", "Sorties moniteur", (h) => h.monitorLabels],
];

function renderLabels() {
  const h = hub();
  const sig = `${h.uniqueId}|${h.videoInputs}|${h.videoOutputs}|${h.monitoringOutputs}`;
  const cols = $("#labels-cols");
  if (sig !== ui.labelsSig) {
    ui.labelsSig = sig;
    cols.innerHTML = "";
    for (const [kind, title, get] of LABEL_KINDS) {
      const list = get(h);
      if (!list.length) continue;
      const col = document.createElement("div");
      col.className = "labels-col";
      col.innerHTML = `<h3>${title} (${list.length})</h3>` + list.map((_, i) =>
        `<div class="label-row"><span class="num">${i + 1}</span><input data-kind="${kind}" data-idx="${i}" maxlength="64" spellcheck="false"></div>`).join("");
      cols.append(col);
    }
  }
  for (const input of cols.querySelectorAll("input")) {
    const current = LABEL_KINDS.find((k) => k[0] === input.dataset.kind)[2](h)[+input.dataset.idx] ?? "";
    input.dataset.orig = current;
    if (!input.classList.contains("dirty") && document.activeElement !== input) input.value = current;
    else if (input.value === current) input.classList.remove("dirty");
  }
  updateLabelButtons();
}

function dirtyLabels() {
  return [...document.querySelectorAll("#labels-cols input.dirty")];
}
function updateLabelButtons() {
  const n = dirtyLabels().length;
  $("#labels-apply").disabled = !n;
  $("#labels-apply").textContent = n ? `Appliquer (${n})` : "Appliquer";
  $("#labels-revert").disabled = !n;
}

function csvCell(v) { return /[",\n;]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v; }
function parseCsv(text) {
  const rows = []; let row = [], cell = "", q = false;
  for (let i = 0; i < text.length; i++) {
    const ch = text[i];
    if (q) {
      if (ch === '"' && text[i + 1] === '"') { cell += '"'; i++; }
      else if (ch === '"') q = false;
      else cell += ch;
    } else if (ch === '"') q = true;
    else if (ch === "," || ch === ";") { row.push(cell); cell = ""; }
    else if (ch === "\n" || ch === "\r") {
      if (ch === "\r" && text[i + 1] === "\n") i++;
      row.push(cell); rows.push(row); row = []; cell = "";
    } else cell += ch;
  }
  if (cell || row.length) { row.push(cell); rows.push(row); }
  return rows;
}

// --- info --------------------------------------------------------------------
function renderInfo() {
  const h = hub(), c = S.connection;
  const dl = (pairs) => `<dl>${pairs.map(([k, v]) => `<dt>${esc(k)}</dt><dd${typeof v === "number" || /^[\d.:A-F]+$/.test(v) ? ' class="mono"' : ""}>${esc(v)}</dd>`).join("")}</dl>`;
  let html = `<div class="info-card"><h3>Appareil</h3>${dl([
    ["Nom", h.friendlyName || "—"], ["Modèle", h.modelName || "—"], ["ID unique", h.uniqueId || "—"],
    ["Adresse", `${c.host}:${c.port}`], ["Protocole", h.protocolVersion || "—"],
  ])}</div>`;
  html += `<div class="info-card"><h3>Entrées / sorties</h3>${dl([
    ["Entrées vidéo", h.videoInputs], ["Sorties vidéo", h.videoOutputs], ["Sorties moniteur", h.monitoringOutputs],
    ["Unités de traitement", h.processingUnits], ["Ports série", h.serialPorts],
    ["Sorties verrouillées", h.locks.filter((l) => l !== "U").length],
  ])}</div>`;
  if (Object.keys(h.configuration).length)
    html += `<div class="info-card"><h3>Configuration</h3>${dl(Object.entries(h.configuration))}</div>`;
  for (const [name, lines] of Object.entries(h.extra))
    html += `<div class="info-card"><h3>${esc(name.toLowerCase())}</h3><pre>${esc(lines.join("\n"))}</pre></div>`;
  $("#info").innerHTML = html;
}

// --- dialogs -------------------------------------------------------------
function askText(title, value = "") {
  const dlg = $("#text-dialog");
  $("#text-dialog-title").textContent = title;
  $("#text-dialog-input").value = value;
  dlg.returnValue = "";
  dlg.showModal();
  $("#text-dialog-input").select();
  return new Promise((resolve) => {
    dlg.addEventListener("close", () => resolve(dlg.returnValue === "ok" ? $("#text-dialog-input").value.trim() : null), { once: true });
  });
}

// --- wiring ------------------------------------------------------------------
function setTab(tab) {
  ui.tab = tab;
  for (const b of document.querySelectorAll(".tabs button")) b.classList.toggle("active", b.dataset.tab === tab);
  for (const p of document.querySelectorAll(".tab-panel")) p.hidden = p.id !== "tab-" + tab;
  for (const g of document.querySelectorAll(".tool-group")) g.hidden = !g.dataset.for.split(" ").includes(tab);
  $("#kind-switch").style.visibility = tab === "panel" || tab === "matrix" ? "" : "hidden";
  if (tab === "labels") ui.labelsSig = "";
  if (S) render();
}

function init() {
  document.documentElement.style.setProperty("--btn-size", ui.size + "px");
  $("#btn-size").value = ui.size;
  $("#take-mode").checked = ui.take;

  $("#connect-form").onsubmit = (e) => {
    e.preventDefault();
    const [host, port] = $("#host-input").value.trim().split(":");
    if (host) api("connect", { host, port: +port || 9990 });
  };
  $("#disconnect-btn").onclick = () => api("disconnect");

  for (const b of document.querySelectorAll(".tabs button")) b.onclick = () => setTab(b.dataset.tab);
  for (const b of document.querySelectorAll("#kind-switch button")) b.onclick = () => {
    ui.kind = b.dataset.kind;
    ui.selected.clear(); ui.armed = null; ui.hover = null;
    for (const x of document.querySelectorAll("#kind-switch button")) x.classList.toggle("active", x === b);
    render();
  };

  $("#take-mode").onchange = (e) => { ui.take = e.target.checked; ui.armed = null; setPref("take", ui.take); render(); };
  $("#filter").oninput = (e) => { ui.filter = e.target.value.trim(); renderPanel(); };
  $("#btn-size").oninput = (e) => {
    ui.size = +e.target.value;
    document.documentElement.style.setProperty("--btn-size", ui.size + "px");
    setPref("size", ui.size);
  };

  $("#dest-grid").onclick = (e) => { const b = e.target.closest(".xbtn"); if (b) selectDest(+b.dataset.out, e); };
  $("#src-grid").onclick = (e) => { const b = e.target.closest(".xbtn"); if (b) clickSource(+b.dataset.in); };
  $("#take-btn").onclick = () => { if (ui.armed !== null) routeSelection(ui.armed); };
  $("#lock-btn").onclick = async () => {
    const action = $("#lock-btn").dataset.action;
    const out = outputs();
    const sel = [...ui.selected];
    if (action === "F" && !confirm("Forcer le déverrouillage de destinations verrouillées par un autre utilisateur ?")) return;
    const locks = action === "O" ? sel.filter((o) => out.locks[o] === "U").map((o) => [o, "O"])
      : action === "U" ? sel.map((o) => [o, "U"])
      : sel.filter((o) => out.locks[o] === "L").map((o) => [o, "F"]);
    if (locks.length) await api("locks", { kind: ui.kind, locks }).catch(() => {});
  };

  document.addEventListener("keydown", (e) => {
    if (e.target.matches("input, textarea") || document.querySelector("dialog[open]")) return;
    if (e.key === "Escape") { ui.selected.clear(); ui.armed = null; if (hub()) render(); }
    if (e.key === "Enter" && ui.take && ui.armed !== null && ui.selected.size) routeSelection(ui.armed);
  });

  // Matrix
  const scroller = $("#matrix-scroll");
  let lastPointer = null;
  scroller.addEventListener("scroll", () => requestAnimationFrame(() => {
    if (lastPointer) { ui.hover = matrixHit(lastPointer); matrixFoot(); }
    drawMatrix();
  }));
  window.addEventListener("resize", () => { if (ui.tab === "matrix" && hub()) drawMatrix(); });
  const canvas = scroller;  // the canvas overlay ignores pointer events
  canvas.addEventListener("mousemove", (e) => {
    lastPointer = { clientX: e.clientX, clientY: e.clientY };
    const hit = matrixHit(e);
    if (JSON.stringify(hit) !== JSON.stringify(ui.hover)) { ui.hover = hit; drawMatrix(); matrixFoot(); }
  });
  canvas.addEventListener("mouseleave", () => { lastPointer = null; ui.hover = null; drawMatrix(); matrixFoot(); });
  canvas.addEventListener("click", (e) => {
    const hit = matrixHit(e);
    if (!hit) return;
    const out = outputs();
    if (out.locks[hit.r] === "L") { toast("Destination verrouillée par un autre utilisateur", true); return; }
    if (out.routing[hit.r] === hit.c) return;
    if (ui.take && !confirm(`Router ${hub().inputLabels[hit.c]} vers ${out.labels[hit.r]} ?`)) return;
    api("route", { kind: ui.kind, routes: [[hit.r, hit.c]] }).catch(() => {});
  });

  // Labels
  $("#labels-cols").addEventListener("input", (e) => {
    const input = e.target;
    input.classList.toggle("dirty", input.value !== input.dataset.orig);
    updateLabelButtons();
  });
  $("#labels-revert").onclick = () => {
    for (const input of dirtyLabels()) { input.value = input.dataset.orig; input.classList.remove("dirty"); }
    updateLabelButtons();
  };
  $("#labels-apply").onclick = async () => {
    const byKind = {};
    for (const input of dirtyLabels()) (byKind[input.dataset.kind] ||= []).push([+input.dataset.idx, input.value.trim()]);
    try {
      for (const [kind, labels] of Object.entries(byKind)) await api("labels", { kind, labels });
      for (const input of dirtyLabels()) input.classList.remove("dirty");
      toast("Étiquettes mises à jour");
    } catch { /* toast already shown */ }
    render();
  };
  $("#labels-export").onclick = () => {
    const h = hub();
    const lines = ["type,numero,etiquette"];
    for (const [kind, , get] of LABEL_KINDS) get(h).forEach((l, i) => lines.push(`${kind},${i + 1},${csvCell(l)}`));
    const blob = new Blob([lines.join("\n") + "\n"], { type: "text/csv" });
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = `${(h.friendlyName || "videohub").replace(/[^\w.-]+/g, "_")}-etiquettes.csv`;
    a.click();
    URL.revokeObjectURL(a.href);
  };
  $("#labels-import").onchange = async (e) => {
    const file = e.target.files[0];
    e.target.value = "";
    if (!file) return;
    let n = 0;
    for (const [kind, num, label] of parseCsv(await file.text())) {
      const input = document.querySelector(`#labels-cols input[data-kind="${CSS.escape((kind || "").trim())}"][data-idx="${(+num || 0) - 1}"]`);
      if (!input || label === undefined) continue;
      input.value = label.trim();
      input.classList.toggle("dirty", input.value !== input.dataset.orig);
      n++;
    }
    updateLabelButtons();
    toast(n ? `${n} étiquette(s) importée(s) — cliquez sur Appliquer` : "Aucune étiquette reconnue dans ce fichier", !n);
  };

  // Sidebar
  $("#discover-btn").onclick = async () => {
    const btn = $("#discover-btn");
    btn.disabled = true; btn.classList.add("busy"); btn.querySelector("span").textContent = "Recherche…";
    try {
      const r = await api("discover");
      if (!r.discovered.length) toast("Aucun Videohub trouvé sur le réseau");
    } catch { /* shown */ }
    btn.disabled = false; btn.classList.remove("busy"); btn.querySelector("span").textContent = "Rechercher sur le réseau";
  };
  $("#save-hub-btn").onclick = async () => {
    const c = S.connection;
    if (!c) return;
    const h = hub();
    const name = await askText("Nom du Videohub", (h && (h.friendlyName || h.modelName)) || c.host);
    if (name) api("hubs/save", { name, host: c.host, port: c.port });
  };
  $("#save-salvo-btn").onclick = () => {
    const n = ui.selected.size;
    $("#salvo-name").value = "";
    $("#salvo-scope-selected").disabled = !n;
    $("#salvo-selected-label").textContent = n ? `Destinations sélectionnées (${n})` : "Destinations sélectionnées (aucune)";
    document.querySelector(`input[name="salvo-scope"][value="${n > 1 ? "selected" : "all"}"]`).checked = true;
    $("#salvo-dialog").returnValue = "";
    $("#salvo-dialog").showModal();
  };
  $("#salvo-dialog").addEventListener("close", () => {
    if ($("#salvo-dialog").returnValue !== "ok") return;
    const scope = document.querySelector('input[name="salvo-scope"]:checked').value;
    const outputs = scope === "all"
      ? { video: "all", monitor: "all" }
      : { [ui.kind]: [...ui.selected] };
    const name = $("#salvo-name").value.trim();
    api("salvos/save", { name, outputs }).then(() => toast(`Salve « ${name} » enregistrée`)).catch(() => {});
  });

  setTab("panel");
  fetch("/api/state").then((r) => r.json()).then((s) => { S = s; render(); }).catch(() => {});
  listen();
}

init();
