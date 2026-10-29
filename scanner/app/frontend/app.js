// ZTG Scanner desktop UI. Talks to the Go side through the Wails bindings
// (window.go.main.App) and runtime events. Every string that came from a
// scanned device is inserted with textContent, never as HTML.
import { showUpdateDialog } from "./updates.js";

(() => {
  const api = window.go && window.go.main && window.go.main.App;
  const rt = window.runtime;
  const $ = (id) => document.getElementById(id);

  const state = {
    hosts: new Map(), // ip -> host
    running: false,
    sortKey: "ip",
    sortDir: 1,
    selected: null,
    defaults: null,
    settings: null,
    renderQueued: false,
    startedAt: 0,
    layout: null, // { order: [keys], hidden: [keys], widths: {key: px} }
    resizedAt: 0,
    dirty: false,
    file: "",
    comparison: null,
    targetSets: [],
    targetSetsError: "",
    targetSetsBusy: false,
    targetSetsReturnFocus: null,
    targetSetDraft: null,
    schedules: [], // saved schedules: { site, name, interval_minutes, desktop_toast, scan }; targets come from the saved set
    schedulesError: "",
    scheduleStatuses: [],
    scheduleNotices: new Map(), // schedule key -> latest new-device notice
    scheduleBusy: false,
    openWith: { commands: [], error: "", editing: -1, busy: false, returnFocus: null },
  };

  // ---------- DOM helpers ----------

  function el(tag, attrs, ...children) {
    const n = document.createElement(tag);
    if (attrs) {
      for (const [k, v] of Object.entries(attrs)) {
        if (v === undefined || v === null || v === false) continue;
        if (k === "class") n.className = v;
        else if (k === "text") n.textContent = v;
        else if (k.startsWith("on")) n.addEventListener(k.slice(2), v);
        else n.setAttribute(k, v === true ? "" : String(v));
      }
    }
    for (const c of children.flat()) {
      if (c === undefined || c === null || c === false) continue;
      n.append(c instanceof Node ? c : document.createTextNode(String(c)));
    }
    return n;
  }

  function toast(msg, ms = 3500) {
    const t = $("toast");
    t.textContent = msg;
    t.hidden = false;
    clearTimeout(toast.timer);
    toast.timer = setTimeout(() => (t.hidden = true), ms);
  }

  function confirmDialog(title, body, okLabel) {
    return new Promise((resolve) => {
      const m = $("modal");
      $("modal-title").textContent = title;
      $("modal-body").textContent = body;
      $("modal-ok").textContent = okLabel;
      m.hidden = false;
      $("modal-cancel").focus();
      const done = (v) => {
        m.hidden = true;
        $("modal-ok").onclick = $("modal-cancel").onclick = null;
        resolve(v);
      };
      $("modal-ok").onclick = () => done(true);
      $("modal-cancel").onclick = () => done(false);
    });
  }

  const store = {
    get(k, d) {
      try {
        const v = localStorage.getItem("ztg-scanner:" + k);
        return v === null ? d : JSON.parse(v);
      } catch {
        return d;
      }
    },
    set(k, v) {
      try {
        localStorage.setItem("ztg-scanner:" + k, JSON.stringify(v));
      } catch {
        /* storage unavailable: settings last for this session only */
      }
    },
  };

  // ---------- theme and window ----------

  // System follows Windows; explicit Light/Dark choices persist across upgrades.
  const THEMES = ["auto", "light", "dark"];
  function applyTheme(choice) {
    const theme = THEMES.includes(choice) ? choice : "auto";
    if (theme === "auto") document.documentElement.removeAttribute("data-theme");
    else document.documentElement.setAttribute("data-theme", theme);
    // The native window behind the page (shown while opening and resizing) takes the page's
    // background, so a light theme never flashes dark.
    if (rt && rt.WindowSetBackgroundColour) {
      const [r, g, b] = (getComputedStyle(document.documentElement).getPropertyValue("--bg").trim().match(/[0-9a-f]{2}/gi) || ["02", "0c", "1b"]).map((h) => parseInt(h, 16));
      rt.WindowSetBackgroundColour(r, g, b, 255);
    }
    const nativeTheme = theme === "dark" ? rt?.WindowSetDarkTheme : theme === "light" ? rt?.WindowSetLightTheme : rt?.WindowSetSystemDefaultTheme;
    nativeTheme?.();
    $("theme").value = theme;
    return theme;
  }
  applyTheme(store.get("theme", "auto"));
  window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => {
    if ($("theme").value === "auto") applyTheme("auto");
  });

  // The desktop window has no native frame (main_windows.go); these are its caption buttons.
  function initTitlebar() {
    if (!rt || !rt.WindowMinimise) return; // browser preview: the browser has its own frame
    document.body.classList.add("has-titlebar");
    $("window-controls").hidden = false;
    const max = $("win-max");
    const showState = async () => {
      const maximized = Boolean(await rt.WindowIsMaximised());
      max.classList.toggle("maximized", maximized);
      const label = maximized ? "Restore" : "Maximize";
      max.title = label;
      max.setAttribute("aria-label", label);
    };
    $("win-min").addEventListener("click", () => rt.WindowMinimise());
    max.addEventListener("click", async () => {
      await rt.WindowToggleMaximise();
      showState();
    });
    $("win-close").addEventListener("click", () => rt.Quit());
    window.addEventListener("resize", showState);
    $("titlebar").addEventListener("dblclick", async (event) => {
      if (getComputedStyle(event.target).getPropertyValue("--wails-draggable").trim() !== "drag") return;
      await rt.WindowToggleMaximise();
      showState();
    });
    showState();
  }

  // ---------- derived fields ----------

  // ipKey is a string that sorts addresses in numeric order: every IPv4 address
  // before every IPv6 one, and IPv6 by its expanded value (a zone sorts last).
  function ipKey(ip) {
    const z = ip.indexOf("%");
    const bare = z < 0 ? ip : ip.slice(0, z);
    const zone = z < 0 ? "" : ip.slice(z);
    if (!bare.includes(":")) return "4" + bare.split(".").map((o) => (Number(o) || 0).toString(16).padStart(2, "0")).join("");
    const [head, tail] = bare.split("::");
    const h = head ? head.split(":") : [];
    const t = tail ? tail.split(":") : [];
    const fill = tail === undefined ? [] : Array(Math.max(0, 8 - h.length - t.length)).fill("0");
    return "6" + [...h, ...fill, ...t].map((g) => g.padStart(4, "0")).join("") + zone;
  }
  const isIPv6 = (ip) => ip.includes(":");
  const cmpKey = (x, y) => (x < y ? -1 : x > y ? 1 : 0);
  const sevRank = { high: 3, medium: 2, info: 1 };
  const ports = (h) => (h.services || []).map((s) => s.port);
  const vendorOf = (h) => (h.mac_randomized ? "(private MAC)" : h.vendor || "");
  const worst = (h) => Math.max(0, ...(h.findings || []).map((f) => sevRank[f.severity] || 0));

  const modelOf = (h) => (h.upnp && [h.upnp.model_name, h.upnp.model_number].filter(Boolean).join(" ")) || (h.snmp && h.snmp.model) || (h.wsd && h.wsd.hardware) || "";
  const llmnrName = (h) => (h.llmnr && h.llmnr.names && h.llmnr.names[0]) || "";
  const signingOf = (s) => (!s ? "" : s.signing_required ? "required" : s.signing_enabled ? "enabled, not required" : "not offered");
  const faviconSummary = (h) => (h.services || []).filter((s) => s.favicon).map((s) => s.port + ":" + s.favicon.sha256.slice(0, 12)).join(" ");
  const serviceSummary = (h) =>
    (h.services || []).map((s) => s.port + "/" + s.name + (s.http && s.http.title ? ' "' + s.http.title + '"' : s.product ? " " + s.product : "")).join("; ");

  // One definition per grid column drives the header, the cells, sorting and the
  // "current view" CSV export. text() is the plain value; cell() overrides display.
  const COLUMNS = [
    { key: "status", label: "", title: "Status", width: 34, fixed: true, text: (h) => h.status, sort: (h) => (h.status === "up" ? 0 : 1),
      cell: (h) => el("span", { class: "dot " + (h.status === "up" ? "up" : ""), title: h.status }) },
    { key: "name", label: "Name", width: 190, text: (h) => h.name || "",
      cell: (h) => el("span", null, h.favorite ? el("span", { class: "favorite-mark", "aria-label": "Favorite", text: "★ " }) : null, h.name || "") },
    { key: "ip", label: "IP address", width: 132, mono: true, text: (h) => h.ip, sort: (h) => ipKey(h.ip) },
    { key: "type", label: "Type", width: 190, text: (h) => h.type || "", tip: (h) => h.type_reason },
    { key: "flags", label: "Findings", width: 160, sort: (h) => -worst(h), cell: findingBadges,
      text: (h) => (h.findings || []).map((f) => f.severity + ":" + f.code).join(" ") },
    { key: "vendor", label: "Manufacturer", width: 210, text: vendorOf },
    { key: "mac", label: "MAC address", width: 140, mono: true, text: (h) => h.mac || "" },
    { key: "ports", label: "Open ports", width: 150, mono: true, text: (h) => ports(h).join(" "), sort: (h) => ports(h).length },
    // Optional columns, hidden until chosen in the Columns menu.
    { key: "ipv6", label: "IPv6 addresses", width: 260, hidden: true, mono: true, text: (h) => (h.ipv6 || []).join(" ") },
    { key: "dns", label: "DNS name", width: 200, hidden: true, text: (h) => h.dns_name || "" },
    { key: "netbios", label: "NetBIOS name", width: 150, hidden: true, text: (h) => (h.netbios && h.netbios.computer) || "" },
    { key: "workgroup", label: "Workgroup", width: 120, hidden: true, text: (h) => (h.netbios && h.netbios.workgroup) || "" },
    { key: "mdns", label: "mDNS name", width: 180, hidden: true, text: (h) => (h.mdns && h.mdns.hostname) || "" },
    { key: "llmnr", label: "LLMNR name", width: 150, hidden: true, text: llmnrName },
    { key: "wsd", label: "WS-Discovery types", width: 220, hidden: true, text: (h) => (h.wsd && (h.wsd.types || []).join(" ")) || "" },
    { key: "smb", label: "SMB dialect", width: 100, hidden: true, text: (h) => (h.smb && h.smb.dialect) || "" },
    { key: "model", label: "Model", width: 170, hidden: true, text: modelOf },
    { key: "serial", label: "Serial", width: 150, hidden: true, mono: true, text: (h) => (h.snmp && h.snmp.serial) || "" },
    { key: "firmware", label: "Firmware", width: 130, hidden: true, text: (h) => (h.snmp && (h.snmp.firmware_rev || h.snmp.software_rev)) || "" },
    { key: "location", label: "Location", width: 150, hidden: true, text: (h) => (h.snmp && h.snmp.sys_location) || "" },
    { key: "os", label: "OS hint", width: 200, hidden: true, text: (h) => h.os_hint || "" },
    { key: "rtt", label: "Ping (ms)", width: 80, hidden: true, mono: true, text: (h) => (h.rtt_ms ? String(h.rtt_ms) : ""), sort: (h) => h.rtt_ms || 1e9 },
    { key: "ttl", label: "TTL", width: 60, hidden: true, mono: true, text: (h) => (h.ttl ? String(h.ttl) : ""), sort: (h) => h.ttl || 999 },
    { key: "services", label: "Services", width: 300, hidden: true, text: serviceSummary },
    { key: "evidence", label: "Found by", width: 160, hidden: true, text: (h) => (h.evidence || []).join(", ") },
    { key: "favorite", label: "Favorite", width: 80, hidden: true, text: (h) => h.favorite ? "Yes" : "" },
    { key: "tags", label: "Tags", width: 200, hidden: true, text: (h) => (h.tags || []).join(", ") },
    { key: "notes", label: "Notes", width: 260, hidden: true, text: (h) => h.notes || "" },
    { key: "lastmac", label: "Last known MAC", width: 150, hidden: true, mono: true, text: (h) => h.last_known_mac || "" },
  ];
  const COL = Object.fromEntries(COLUMNS.map((c) => [c.key, c]));
  const MIN_WIDTH = 44;
  const MAX_WIDTH = 900;

  function sortValue(col, h) {
    if (col.sort) return col.sort(h);
    const t = col.text(h);
    return t ? t.toLowerCase() : "\uffff"; // empty values sort last
  }

  function defaultLayout() {
    return { order: COLUMNS.map((c) => c.key), hidden: COLUMNS.filter((c) => c.hidden).map((c) => c.key), widths: {} };
  }

  // Saved layouts are merged with the current column list, so columns added in a
  // later version appear (with their default visibility) and removed ones vanish.
  function loadLayout() {
    const d = defaultLayout();
    const saved = store.get("layout", null);
    if (!saved || !Array.isArray(saved.order)) return d;
    const known = (k) => Object.prototype.hasOwnProperty.call(COL, k);
    const order = saved.order.filter(known);
    for (const k of d.order) if (!order.includes(k)) order.push(k);
    const savedHidden = Array.isArray(saved.hidden) ? saved.hidden.filter(known) : [];
    const added = d.order.filter((k) => !saved.order.includes(k));
    const hidden = [...new Set([...savedHidden, ...added.filter((k) => COL[k].hidden)])].filter((k) => k !== "status");
    const widths = {};
    for (const [k, w] of Object.entries(saved.widths || {})) {
      if (known(k) && Number.isFinite(w)) widths[k] = Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, Math.round(w)));
    }
    return { order, hidden, widths };
  }

  const saveLayout = () => store.set("layout", state.layout);
  const visibleCols = () => state.layout.order.filter((k) => !state.layout.hidden.includes(k)).map((k) => COL[k]);
  const widthOf = (c) => state.layout.widths[c.key] || c.width;

  function matches(h, q) {
    if (!q) return true;
    const hay = [
      h.ip, ...(h.ipv6 || []), h.name, h.mac, h.vendor, h.type, h.dns_name, h.notes, ...(h.tags || []),
      h.netbios && h.netbios.computer, h.netbios && h.netbios.workgroup,
      h.mdns && h.mdns.hostname, llmnrName(h), h.wsd && h.wsd.name, h.upnp && h.upnp.manufacturer, h.upnp && h.upnp.model_name,
      h.snmp && h.snmp.sys_name, h.snmp && h.snmp.model, h.snmp && h.snmp.serial, h.snmp && h.snmp.sys_location,
      ...(h.services || []).flatMap((s) => [String(s.port), s.name, s.product, s.http && s.http.title]),
    ].filter(Boolean).join(" ").toLowerCase();
    return q.split(/\s+/).every((term) => hay.includes(term));
  }

  // ---------- rendering ----------

  function visibleHosts() {
    const q = $("filter").value.trim().toLowerCase();
    const type = $("type-filter").value;
    const findingsOnly = $("findings-only").checked;
    const showDown = $("show-down").checked;
    const col = COL[state.sortKey] || COL.ip;
    const key = (h) => sortValue(col, h);
    return [...state.hosts.values()]
      .filter((h) => (showDown || h.status === "up") && (!type || h.type === type) && (!findingsOnly || (h.findings || []).length) && (!$("favorites-only").checked || h.favorite) && matches(h, q))
      .sort((a, b) => {
        const x = key(a), y = key(b);
        return (x < y ? -1 : x > y ? 1 : cmpKey(ipKey(a.ip), ipKey(b.ip))) * state.sortDir;
      });
  }

  function findingBadges(h) {
    return (h.findings || []).map((f) => el("span", { class: "badge " + f.severity, title: f.message, text: f.code }));
  }

  function cell(c, h) {
    if (c.cell) return el("td", { "data-col": c.key }, c.cell(h));
    const t = c.text(h);
    return el("td", { "data-col": c.key, class: c.mono ? "mono" : null, title: (c.tip && c.tip(h)) || t || null, text: t });
  }

  function row(h, cols) {
    return el("tr", { class: (h.status === "up" ? "" : "down ") + (state.selected === h.ip ? "selected" : ""), "data-ip": h.ip, tabindex: 0 },
      cols.map((c) => cell(c, h)));
  }

  // ---------- columns: header, resizing, chooser ----------

  function applyTableWidth() {
    const total = visibleCols().reduce((n, c) => n + widthOf(c), 0);
    $("grid").style.width = total + "px";
  }

  function renderHead() {
    const tr = el("tr");
    for (const c of visibleCols()) {
      const th = el("th", { "data-sort": c.key, "data-col": c.key, title: c.title || c.label },
        el("span", { class: "th-label", text: c.label }));
      // Set through CSSOM: the Content-Security-Policy blocks inline style attributes.
      th.style.width = widthOf(c) + "px";
      if (c.title && !c.label) th.setAttribute("aria-label", c.title);
      if (!c.fixed) {
        th.append(el("span", { class: "resize", role: "separator", "aria-orientation": "vertical", "aria-label": "Resize " + c.label + " column", title: "Drag to resize, double-click to fit" }));
      }
      tr.append(th);
    }
    $("grid").querySelector("thead").replaceChildren(tr);
    applyTableWidth();
  }

  function setWidth(key, w) {
    const px = Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, Math.round(w)));
    state.layout.widths[key] = px;
    const th = $("grid").querySelector('th[data-col="' + key + '"]');
    if (th) th.style.width = px + "px";
    applyTableWidth();
  }

  // Double-click a handle: fit the column to its widest visible value.
  function autoFit(key) {
    const probe = el("span", { class: "measure" });
    document.body.append(probe);
    let w = 0;
    const th = $("grid").querySelector('th[data-col="' + key + '"] .th-label');
    if (th) {
      probe.className = "measure th";
      probe.textContent = th.textContent;
      w = probe.offsetWidth + 18;
    }
    for (const td of $("rows").querySelectorAll('td[data-col="' + key + '"]')) {
      probe.className = "measure" + (td.classList.contains("mono") ? " mono" : "");
      probe.replaceChildren(...[...td.childNodes].map((n) => n.cloneNode(true)));
      w = Math.max(w, probe.offsetWidth);
    }
    probe.remove();
    setWidth(key, w + 22); // cell padding
    saveLayout();
  }

  function startResize(e, handle) {
    const th = handle.closest("th");
    const key = th.dataset.col;
    const startX = e.clientX;
    const startW = th.getBoundingClientRect().width;
    handle.setPointerCapture(e.pointerId);
    document.body.classList.add("resizing");
    const move = (ev) => setWidth(key, startW + ev.clientX - startX);
    const up = () => {
      handle.removeEventListener("pointermove", move);
      handle.removeEventListener("pointerup", up);
      handle.removeEventListener("pointercancel", up);
      document.body.classList.remove("resizing");
      state.resizedAt = Date.now();
      saveLayout();
    };
    handle.addEventListener("pointermove", move);
    handle.addEventListener("pointerup", up);
    handle.addEventListener("pointercancel", up);
  }

  function renderColumnMenu() {
    const menu = $("columns-menu");
    const items = state.layout.order.filter((k) => k !== "status").map((k) => {
      const c = COL[k];
      const box = el("input", { type: "checkbox", "data-col": k });
      box.checked = !state.layout.hidden.includes(k);
      return el("li", { role: "none" }, el("label", { class: "check menu-check" }, box, c.label));
    });
    menu.replaceChildren(
      ...items,
      el("li", { role: "separator", class: "menu-sep" }),
      el("li", { role: "none" }, el("button", { role: "menuitem", id: "columns-fit", text: "Fit all columns to content" })),
      el("li", { role: "none" }, el("button", { role: "menuitem", id: "columns-reset", text: "Reset columns" })),
    );
  }

  function toggleColumn(key, show) {
    const hidden = new Set(state.layout.hidden);
    if (show) hidden.delete(key);
    else hidden.add(key);
    if (visibleCols().length <= 2 && !show) return; // keep the grid usable
    state.layout.hidden = [...hidden];
    saveLayout();
    renderHead();
    render();
  }

  function render() {
    state.renderQueued = false;
    const list = visibleHosts();
    const cols = visibleCols();
    $("rows").replaceChildren(...list.map((h) => row(h, cols)));
    $("empty").hidden = list.length > 0;
    $("empty").textContent = state.hosts.size ? "No devices match the filters." : state.running ? "Scanning…" : "No results yet.";
    const up = [...state.hosts.values()].filter((h) => h.status === "up").length;
    $("count").textContent = list.length + " shown · " + up + " up";
    refreshTypeFilter();
    for (const th of document.querySelectorAll("thead th")) {
      th.removeAttribute("aria-sort");
      if (th.dataset.sort === state.sortKey) th.setAttribute("aria-sort", state.sortDir > 0 ? "ascending" : "descending");
    }
  }

  function queueRender() {
    if (!state.renderQueued) {
      state.renderQueued = true;
      requestAnimationFrame(render);
    }
  }

  function refreshTypeFilter() {
    const sel = $("type-filter");
    const types = [...new Set([...state.hosts.values()].map((h) => h.type).filter(Boolean))].sort();
    const have = [...sel.options].slice(1).map((o) => o.value);
    if (types.join("|") === have.join("|")) return;
    const cur = sel.value;
    sel.replaceChildren(el("option", { value: "", text: "All types" }), ...types.map((t) => el("option", { value: t, text: t })));
    sel.value = types.includes(cur) ? cur : "";
  }

  function kv(pairs) {
    const dl = el("dl");
    for (const [k, v] of pairs) {
      if (v === undefined || v === null || v === "" || (Array.isArray(v) && !v.length)) continue;
      dl.append(el("dt", { text: k }), el("dd", null, v instanceof Node ? v : String(v)));
    }
    return dl.childElementCount ? dl : null;
  }

  function section(title, ...content) {
    const items = content.flat().filter(Boolean);
    return items.length ? [el("h3", { text: title }), ...items] : [];
  }

  function copyButton(text) {
    return el("button", {
      class: "link", title: "Copy", text: "copy",
      onclick: async () => {
        try {
          if (rt && rt.ClipboardSetText) await rt.ClipboardSetText(text);
          else await navigator.clipboard.writeText(text);
          toast("Copied " + text);
        } catch {
          toast("Could not copy");
        }
      },
    });
  }

  function serviceCard(h, s) {
    const web = s.http && (s.name === "http" || s.name === "https");
    return el("div", { class: "svc" },
      el("div", { class: "svc-head" },
        el("span", { class: "mono", text: s.port + "/" + s.name }),
        web && api && !h.ip.includes("%") ? el("button", { class: "link", text: "Open in browser", onclick: () => api.OpenWeb(h.ip, s.port).catch((e) => toast(String(e))) }) : null),
      kv([
        ["Product", s.product],
        ["Banner", s.banner],
        ["HTTP status", s.http && s.http.status],
        ["Page title", s.http && s.http.title],
        ["Server", s.http && s.http.server],
        ["Auth realm", s.http && s.http.realm],
        ["Redirects to", s.http && s.http.location],
        ["TLS", s.tls && s.tls.version],
        ["Certificate", s.tls && s.tls.subject],
        ["Issuer", s.tls && (s.tls.self_signed ? "self-signed" : s.tls.issuer)],
        ["Names", s.tls && (s.tls.dns_names || []).join(", ")],
        ["Expires", s.tls && s.tls.not_after && s.tls.not_after.slice(0, 10)],
        ["Icon SHA-256", s.favicon && s.favicon.sha256],
        ["Icon MurmurHash3", s.favicon && String(s.favicon.mmh3)],
        ["Icon type", s.favicon && [s.favicon.format, s.favicon.size ? s.favicon.size + " bytes" : ""].filter(Boolean).join(", ")],
      ]),
    );
  }

  function renderDetail() {
    const pane = $("detail");
    const h = state.selected && state.hosts.get(state.selected);
    if (!h) {
      pane.hidden = true;
      pane.replaceChildren();
      return;
    }
    pane.hidden = false;
    const nb = h.netbios, md = h.mdns, up = h.upnp;
    pane.replaceChildren(
      el("button", { class: "ghost close", "aria-label": "Close details", text: "✕", onclick: () => select(null) }),
      el("h2", { text: h.name || h.ip }),
      el("div", { class: "muted mono" }, h.ip, " ", copyButton(h.ip)),
      ...(h.ipv6 || []).filter((a) => a !== h.ip).map((a) => el("div", { class: "muted mono", "data-ipv6": a }, a, " ", copyButton(a))),
      deviceActions(h),
      ...section("Device", kv([
        ["Type", h.type],
        ["Why", h.type_reason],
        ["Status", h.status + (h.evidence && h.evidence.length ? " (" + h.evidence.join(", ") + ")" : "")],
        ["Round trip", h.rtt_ms ? h.rtt_ms + " ms" : ""],
        ["TTL", h.ttl || ""],
        ["OS hint", h.os_hint],
      ])),
      ...section("Hardware", kv([
        ["MAC address", h.mac ? el("span", { class: "mono" }, h.mac, " ", copyButton(h.mac)) : ""],
        ["Last known MAC", h.last_known_mac],
        ["Manufacturer", h.mac_randomized ? "Private (randomized) MAC address: no manufacturer" : h.vendor],
        ["UPnP maker", up && up.manufacturer],
        ["Model", (up && [up.model_name, up.model_number].filter(Boolean).join(" ")) || (h.snmp && h.snmp.model)],
        ["UPnP type", up && up.device_type],
      ])),
      annotationForm(h),
      ...section("Names", kv([
        ["DNS", h.dns_name],
        ["NetBIOS", nb && nb.computer],
        ["Workgroup", nb && nb.workgroup],
        ["mDNS", md && md.hostname],
        ["LLMNR", h.llmnr && (h.llmnr.names || []).join(", ")],
        ["UPnP name", up && up.friendly_name],
        ["UPnP server", up && up.server],
      ])),
      ...section("SNMP", h.snmp ? kv([
        ["System name", h.snmp.sys_name],
        ["Description", h.snmp.sys_descr],
        ["Vendor", [h.snmp.manufacturer, h.snmp.enterprise].filter(Boolean).join(" · ")],
        ["Model", h.snmp.model],
        ["Serial", h.snmp.serial ? el("span", { class: "mono" }, h.snmp.serial, " ", copyButton(h.snmp.serial)) : ""],
        ["Firmware", h.snmp.firmware_rev || h.snmp.software_rev],
        ["Hardware rev", h.snmp.hardware_rev],
        ["Location", h.snmp.sys_location],
        ["Contact", h.snmp.sys_contact],
        ["Interfaces", h.snmp.interfaces],
        ["Uptime", h.snmp.uptime_seconds ? Math.floor(h.snmp.uptime_seconds / 86400) + " days" : ""],
        ["Object ID", h.snmp.sys_object_id],
      ]) : null),
      // Everything in the next two sections is what the device itself reported.
      ...section("WS-Discovery (device-reported)", h.wsd ? kv([
        ["Types", (h.wsd.types || []).join(" ")],
        ["ONVIF name", h.wsd.name],
        ["ONVIF hardware", h.wsd.hardware],
        ["Scopes", (h.wsd.scopes || []).join("  ")],
        ["Announced addresses (not contacted)", (h.wsd.xaddrs || []).join("  ")],
      ]) : null),
      ...section("SMB2 negotiate (server-reported)", h.smb ? kv([
        ["Highest dialect", h.smb.dialect],
        ["Era (inferred from dialect)", h.smb.era_hint],
        ["Signing", signingOf(h.smb)],
        ["Capabilities", (h.smb.capabilities || []).join(", ")],
        ["Server clock (UTC)", h.smb.system_time],
        ["Server start time (UTC)", h.smb.start_time],
        ["Server GUID", h.smb.server_guid],
      ]) : null),
      ...section("Advertised services (mDNS)", md && md.services && md.services.length ? el("div", { class: "chips" }, md.services.map((s) => el("span", { text: s }))) : null),
      ...section("Open ports", (h.services || []).map((s) => serviceCard(h, s))),
      ...section("Findings", (h.findings || []).map((f) => el("div", { class: "finding " + f.severity }, el("strong", { text: f.severity.toUpperCase() + " " }), f.message))),
      ...section("NetBIOS name table", nb && nb.names && nb.names.length
        ? el("div", { class: "chips" }, nb.names.map((n) => el("span", { class: "mono", text: n.name + " <" + n.suffix.toString(16).padStart(2, "0").toUpperCase() + ">" + (n.group ? " group" : "") })))
        : null),
    );
  }

  function select(ip) {
    state.selected = ip;
    for (const tr of $("rows").children) tr.classList.toggle("selected", tr.dataset.ip === ip);
    renderDetail();
  }

  // ---------- local inventories and actions ----------

  function markDirty(value = true) {
    state.dirty = value;
    $("scan-file").textContent = (state.file || "Not saved") + (value ? " · unsaved changes" : "");
    $("scan-file").title = $("scan-file").textContent;
  }

  function deviceActions(h) {
    // The connection actions take an IPv4 address; a device found only over IPv6
    // is listed and probed, but not offered them.
    const v6only = isIPv6(h.ip);
    const buttons = [
      ["ping", "Ping", true], ["traceroute", "Trace route", true],
      ["ssh", "SSH", h.status === "up" && ports(h).includes(22)],
      ["rdp", "RDP", h.status === "up" && ports(h).includes(3389)],
    ].map(([action, label, enabled]) => el("button", {
      type: "button", "data-action": action, text: label, disabled: !enabled || v6only,
      title: v6only ? "Connection actions need an IPv4 address" : null,
      onclick: () => runAction(action, h.ip),
    }));
    buttons.push(el("button", {
      type: "button", "data-action": "open-with", "aria-haspopup": "menu", text: "Open with ▾", disabled: !api || v6only,
      onclick: (e) => {
        e.stopPropagation();
        const r = e.currentTarget.getBoundingClientRect();
        showOpenWithMenu(h.ip, r.left, r.bottom + 4);
      },
    }));
    const wakeMAC = h.mac || h.last_known_mac;
    buttons.push(el("button", { type: "button", "data-action": "wake", text: "Wake-on-LAN", disabled: !wakeMAC || h.mac_randomized || v6only,
      onclick: async () => {
        if (!await confirmDialog("Send Wake-on-LAN?", "Send one wake packet for " + h.ip + " (" + (h.mac ? "MAC " : "last known MAC ") + wakeMAC + ") on its directly connected network?", "Send wake packet")) return;
        try { await api.WakeOnLAN(h.ip); toast("Wake packet sent. The device must support and enable Wake-on-LAN."); }
        catch (e) { toast(String(e)); }
      },
    }));
    return el("div", { class: "device-actions" }, buttons);
  }

  async function runAction(action, ip, allowPublic = false) {
    const diagnostic = action === "ping" || action === "traceroute";
    if (diagnostic) {
      $("diagnostics").hidden = false;
      $("comparison").hidden = true;
      $("open-with").hidden = true;
      $("diagnostics-title").textContent = (action === "ping" ? "Ping " : "Trace route to ") + ip;
      $("diagnostics-output").textContent = "Running…";
    }
    try {
      const output = await api.DeviceAction(action, ip, allowPublic);
      if (diagnostic) $("diagnostics-output").textContent = output;
      else toast(output);
    } catch (e) {
      const message = String(e && e.message ? e.message : e);
      if (!allowPublic && message.includes("public address space")) {
        const ok = await confirmDialog("Connect to a public address?", "Only continue if you are authorized to use " + action + " with " + ip + ".", "I am authorized");
        if (ok) return runAction(action, ip, true);
      }
      if (diagnostic) $("diagnostics-output").textContent = message;
      else toast(message);
    }
  }

  function annotationForm(h) {
    const notes = el("textarea", { id: "device-notes", rows: 3, maxlength: 8192 });
    notes.value = h.notes || "";
    const tags = el("input", { id: "device-tags", type: "text", placeholder: "owner:Alice, location:MDF, cui:inside" });
    tags.value = (h.tags || []).join(", ");
    const favorite = el("input", { id: "device-favorite", type: "checkbox" });
    favorite.checked = !!h.favorite;
    const button = el("button", { id: "apply-notes", type: "button", text: "Apply device notes", disabled: state.running || (h.status !== "up" && !h.last_known_mac),
      onclick: async () => {
        const note = { notes: notes.value, tags: [...new Set(tags.value.split(",").map(t => t.trim()).filter(Boolean))], favorite: favorite.checked };
        try {
          await api.SetAnnotation(h.ip, note);
          Object.assign(h, note);
          markDirty(); render();
          toast("Device updated. Save the scan to keep your notes and favorites.");
        } catch (e) { toast(String(e)); }
      },
    });
    return el("div", { class: "annotation-form" },
      el("label", null, "Device notes", notes), el("label", null, "Tags (comma separated)", tags),
      el("label", { class: "check" }, favorite, "Favorite"), button);
  }

  async function replaceInventory(action, options = {}) {
    if (state.targetSetsBusy && action !== "target-set") return false;
    if (!options.confirmed && state.dirty && !await confirmDialog("Replace current results?", "The current scan has unsaved changes. Save the scan first if you want to keep them.", "Replace results")) return false;
    try {
      let path = "";
      if (action === "load") { path = await api.LoadScan(); if (!path) return; }
      else await api.NewSite();
      if (action === "target-set") await api.SetSite(state.targetSetDraft.site);
      state.file = path;
      state.settings.probes.snmp = false;
      $("snmp-user").value = "";
      $("snmp-auth-pass").value = "";
      $("snmp-priv-pass").value = "";
      paintSettings();
      state.comparison = null;
      $("comparison").hidden = true;
      $("diagnostics").hidden = true;
      select(null);
      const snap = await api.Results(true);
      $("site").value = snap.site || "";
      if (action === "target-set") {
        $("site").value = state.targetSetDraft.site;
        $("targets").value = state.targetSetDraft.targets;
      } else if ((snap.targets || []).length) $("targets").value = snap.targets.join(", ");
      state.hosts.clear();
      for (const h of snap.hosts || []) state.hosts.set(h.ip, h);
      markDirty(false); render();
      $("status-text").textContent = path ? "Opened " + path : "Ready for a new site.";
      $("warnings").textContent = "";
      $("progress-bar").style.width = "0%";
      return true;
    } catch (e) { toast(String(e)); return false; }
  }

  // ---------- local saved target sets ----------

  const TARGET_SETS_KEY = "ztg-scanner:target-sets";
  const TARGET_SET_LIMITS = { site: 200, name: 100, targets: 64 * 1024 };
  const byteLength = (value) => new TextEncoder().encode(value).length;
  const targetSetKey = (site, name) => site.toLowerCase() + "\u0000" + name.toLowerCase();

  function normalizeTargetSetLabel(value, field) {
    const limit = TARGET_SET_LIMITS[field];
    if (/[\r\n\0]/.test(value)) throw new Error("The " + (field === "site" ? "client / site label" : "set name") + " must be on one line without control characters.");
    const label = value.trim();
    if (!label) throw new Error("Enter a " + (field === "site" ? "client / site label" : "set name") + ".");
    if (byteLength(label) > limit) throw new Error("The " + (field === "site" ? "client / site label" : "set name") + " must be at most " + limit + " UTF-8 bytes.");
    return label;
  }

  function validateTargetSetShape(value) {
    if (!value || typeof value !== "object" || Array.isArray(value) || value.version !== 1 || !Array.isArray(value.sets) || value.sets.length > 50 || Object.keys(value).some((key) => !["version", "sets"].includes(key))) {
      throw new Error("Saved target sets have an unsupported or malformed format. The stored data was left untouched.");
    }
    const seen = new Set();
    const sets = value.sets.map((entry) => {
      if (!entry || typeof entry !== "object" || Array.isArray(entry) || typeof entry.site !== "string" || typeof entry.name !== "string" || typeof entry.targets !== "string") {
        throw new Error("A saved target set is malformed. The stored data was left untouched.");
      }
      if (Object.keys(entry).some((key) => !["site", "name", "targets"].includes(key))) throw new Error("A saved target set has unexpected fields. The stored data was left untouched.");
      const site = entry.site.trim();
      const name = entry.name.trim();
      if (!site || !name || !entry.targets.trim() || /[\r\n\0]/.test(entry.site) || /[\r\n\0]/.test(entry.name) || byteLength(entry.site) > TARGET_SET_LIMITS.site || byteLength(entry.name) > TARGET_SET_LIMITS.name || byteLength(entry.targets) > TARGET_SET_LIMITS.targets) {
        throw new Error("A saved target set exceeds its limits or has a blank label. The stored data was left untouched.");
      }
      const key = targetSetKey(site, name);
      if (seen.has(key)) throw new Error("Saved target sets contain duplicate client and set names. The stored data was left untouched.");
      seen.add(key);
      return { site, name, targets: entry.targets };
    });
    return sets;
  }

  function readTargetSets() {
    state.targetSetsError = "";
    state.targetSets = [];
    try {
      const raw = localStorage.getItem(TARGET_SETS_KEY);
      if (raw === null) return [];
      state.targetSets = validateTargetSetShape(JSON.parse(raw));
      return state.targetSets;
    } catch (e) {
      state.targetSetsError = String(e && e.message ? e.message : e);
      state.targetSets = [];
      return [];
    }
  }

  function targetSetStatus(message = state.targetSetsError) {
    $("target-set-status").textContent = state.targetSetsError && message !== state.targetSetsError
      ? state.targetSetsError + (message ? " " + message : "")
      : message;
  }

  function setTargetSetBusy(busy) {
    state.targetSetsBusy = busy;
    updateTargetSetControls();
  }

  function updateTargetSetControls() {
    const locked = state.running || state.targetSetsBusy;
    for (const id of ["target-set-site", "target-set-name", "target-set-targets", "target-set-save", "target-set-load", "target-set-delete", "target-set-import", "target-set-list", "target-set-reset"]) {
      $(id).disabled = locked;
    }
    $("target-set-save").disabled ||= !!state.targetSetsError;
    $("target-set-reset").hidden = !state.targetSetsError;
    for (const id of ["site", "save-scan", "load-scan", "compare-scan", "new-site"]) $(id).disabled = locked;
    $("targets").disabled = locked;
    $("suggest-btn").disabled = locked;
    $("scan-btn").disabled = state.targetSetsBusy;
  }

  function renderTargetSets(selectedKey = "") {
    const list = $("target-set-list");
    list.replaceChildren();
    state.targetSets.forEach((entry, index) => {
      const option = document.createElement("option");
      option.value = String(index);
      option.textContent = entry.site + " · " + entry.name;
      option.dataset.key = targetSetKey(entry.site, entry.name);
      list.append(option);
    });
    const selected = [...list.options].find((option) => option.dataset.key === selectedKey);
    if (selected) list.value = selected.value;
    updateTargetSetControls();
  }

  function writeTargetSets(sets) {
    const value = { version: 1, sets };
    // Validate our own output and verify the write so storage failures are visible.
    validateTargetSetShape(value);
    const serialized = JSON.stringify(value);
    localStorage.setItem(TARGET_SETS_KEY, serialized);
    if (localStorage.getItem(TARGET_SETS_KEY) !== serialized) throw new Error("Saved target sets could not be verified after writing.");
    state.targetSets = sets;
    state.targetSetsError = "";
    targetSetStatus("");
    // A schedule follows its saved set: a changed set is rescheduled with its new targets, a deleted one is dropped.
    if (api && state.schedules.length) pushSchedules().catch((e) => scheduleStatus(errText(e)));
  }

  async function normalizeDraftTargets(value) {
    if (byteLength(value) > TARGET_SET_LIMITS.targets) throw new Error("Networks must be 64 KiB or smaller.");
    const normalized = await api.ValidateTargets(value);
    if (typeof normalized !== "string" || !normalized.trim()) throw new Error("Enter at least one network.");
    if (byteLength(normalized) > TARGET_SET_LIMITS.targets) throw new Error("Normalized networks exceed the 64 KiB limit.");
    return normalized;
  }

  function openTargetSets() {
    if ($("target-sets").hidden) {
      state.targetSetsReturnFocus = document.activeElement;
      closeMenus();
      for (const id of ["settings", "comparison", "diagnostics", "open-with"]) $(id).hidden = true;
      $("settings-btn").setAttribute("aria-expanded", "false");
      $("target-set-site").value = $("site").value;
      $("target-set-name").value = "";
      $("target-set-targets").value = $("targets").value;
      readTargetSets();
      renderTargetSets();
      targetSetStatus();
      fillScheduleForm(null);
      $("target-sets").hidden = false;
      $("target-sets-btn").setAttribute("aria-expanded", "true");
      $("target-set-list").focus();
    }
  }

  function closeTargetSets() {
    if ($("target-sets").hidden) return;
    $("target-sets").hidden = true;
    $("target-sets-btn").setAttribute("aria-expanded", "false");
    if (state.targetSetsReturnFocus && state.targetSetsReturnFocus.focus) state.targetSetsReturnFocus.focus();
    state.targetSetsReturnFocus = null;
  }

  function fillTargetSetDraft(entry) {
    if (!entry) return;
    $("target-set-site").value = entry.site;
    $("target-set-name").value = entry.name;
    $("target-set-targets").value = entry.targets;
    targetSetStatus("");
  }

  async function saveTargetSet() {
    if (!api || state.running || state.targetSetsBusy || state.targetSetsError) return;
    let site, name;
    try {
      site = normalizeTargetSetLabel($("target-set-site").value, "site");
      name = normalizeTargetSetLabel($("target-set-name").value, "name");
    } catch (e) { return targetSetStatus(String(e && e.message ? e.message : e)); }
    $("target-set-site").value = site;
    $("target-set-name").value = name;
    const inputTargets = $("target-set-targets").value;
    setTargetSetBusy(true);
    try {
      const targets = await normalizeDraftTargets(inputTargets);
      const key = targetSetKey(site, name);
      const existing = state.targetSets.findIndex((entry) => targetSetKey(entry.site, entry.name) === key);
      if (existing >= 0 && !await confirmDialog("Replace set?", "A saved set with this client and name already exists. Replace it?", "Replace set")) return;
      const sets = [...state.targetSets];
      const entry = { site, name, targets };
      if (existing >= 0) sets[existing] = entry;
      else if (sets.length >= 50) throw new Error("You can save up to 50 target sets.");
      else sets.push(entry);
      writeTargetSets(sets);
      renderTargetSets(key);
      targetSetStatus("Target set saved.");
    } catch (e) {
      targetSetStatus(String(e && e.message ? e.message : e));
    } finally { setTargetSetBusy(false); }
  }

  async function importTargetSetFile() {
    if (!api || state.running || state.targetSetsBusy) return;
    setTargetSetBusy(true);
    try {
      const imported = await api.ImportTargets();
      if (imported === "") return;
      if (typeof imported !== "string") throw new Error("The imported target list was not valid text.");
      const normalized = await normalizeDraftTargets(imported);
      $("target-set-targets").value = normalized;
      targetSetStatus("Imported targets into the draft. Save the set or use targets when ready.");
    } catch (e) { targetSetStatus(String(e && e.message ? e.message : e)); }
    finally { setTargetSetBusy(false); }
  }

  async function useTargetSetDraft() {
    if (!api || state.running || state.targetSetsBusy) return;
    setTargetSetBusy(true);
    try {
      const site = $("target-set-site").value;
      const normalizedSite = normalizeTargetSetLabel(site, "site");
      const normalizedName = normalizeTargetSetLabel($("target-set-name").value, "name");
      const targets = await normalizeDraftTargets($("target-set-targets").value);
      if (state.dirty && !await confirmDialog("Replace current results?", "Using these targets clears the current inventory and its notes. Save the scan first if you want to keep them.", "Replace results")) return;
      state.targetSetDraft = { site: normalizedSite, name: normalizedName, targets };
      $("target-set-site").value = normalizedSite;
      $("target-set-name").value = normalizedName;
      if (await replaceInventory("target-set", { confirmed: true })) closeTargetSets();
    } catch (e) { targetSetStatus(String(e && e.message ? e.message : e)); }
    finally { setTargetSetBusy(false); }
  }

  async function deleteTargetSet() {
    if (state.running || state.targetSetsBusy || state.targetSetsError) return;
    const option = $("target-set-list").selectedOptions[0];
    const entry = option && state.targetSets[Number(option.value)];
    if (!entry) return targetSetStatus("Choose a saved set to delete.");
    setTargetSetBusy(true);
    try {
      if (!await confirmDialog("Delete target set?", "Delete “" + entry.name + "” for “" + entry.site + "”?", "Delete set")) return;
      const key = targetSetKey(entry.site, entry.name);
      writeTargetSets(state.targetSets.filter((candidate) => targetSetKey(candidate.site, candidate.name) !== key));
      renderTargetSets();
      targetSetStatus("Target set deleted.");
    } catch (e) { targetSetStatus(String(e && e.message ? e.message : e)); }
    finally { setTargetSetBusy(false); }
  }

  async function resetTargetSets() {
    if (!state.targetSetsError || state.running || state.targetSetsBusy) return;
    setTargetSetBusy(true);
    try {
      if (!await confirmDialog("Reset saved target sets?", "This permanently removes the malformed saved target data from this app.", "Reset saved sets")) return;
      localStorage.removeItem(TARGET_SETS_KEY);
      if (localStorage.getItem(TARGET_SETS_KEY) !== null) throw new Error("Saved target sets could not be removed from local storage.");
      state.targetSets = [];
      state.targetSetsError = "";
      renderTargetSets();
      targetSetStatus("Saved target sets were reset.");
      if (api && state.schedules.length) pushSchedules().catch((e) => scheduleStatus(errText(e)));
    } catch (e) {
      state.targetSetsError = String(e && e.message ? e.message : e);
      targetSetStatus();
    } finally { setTargetSetBusy(false); }
  }

  // ---------- scheduled rescans (SC7) ----------
  //
  // Opt-in per saved target set, off by default, and only while this app is open. Schedules live in this
  // WebView's local storage beside the saved sets (they carry no targets: those come from the set) and are
  // handed to the app on every change and at launch. The app validates them again, so a refusal here is a
  // convenience, never the guard: a public range is refused in Go whatever this page sends.

  const SCHEDULES_KEY = "ztg-scanner:schedules";
  const SCHEDULE_MAX = 20;
  const SCHEDULE_MIN_MINUTES = 15;
  const SCHEDULE_MAX_MINUTES = 1440;
  const scheduleKey = (site, name) => targetSetKey(site, name);

  function validScanShape(scan) {
    if (!scan || typeof scan !== "object" || Array.isArray(scan)) return false;
    if (Object.keys(scan).some((k) => !["mode", "ports", "probes", "concurrency", "timeout_ms"].includes(k))) return false;
    if (typeof scan.mode !== "string" || typeof scan.ports !== "string" || !Number.isInteger(scan.concurrency) || !Number.isInteger(scan.timeout_ms)) return false;
    return !!scan.probes && typeof scan.probes === "object" && !Array.isArray(scan.probes) && Object.values(scan.probes).every((v) => typeof v === "boolean");
  }

  function validateSchedulesShape(value) {
    const bad = "Saved schedules have an unsupported or malformed format. The stored data was left untouched and scheduled rescans are off until it is reset.";
    if (!value || typeof value !== "object" || Array.isArray(value) || value.version !== 1 || !Array.isArray(value.schedules) || value.schedules.length > SCHEDULE_MAX || Object.keys(value).some((k) => !["version", "schedules"].includes(k))) throw new Error(bad);
    const seen = new Set();
    return value.schedules.map((e) => {
      if (!e || typeof e !== "object" || Array.isArray(e) || Object.keys(e).some((k) => !["site", "name", "interval_minutes", "desktop_toast", "scan"].includes(k))
        || typeof e.site !== "string" || typeof e.name !== "string" || !Number.isInteger(e.interval_minutes) || e.interval_minutes < SCHEDULE_MIN_MINUTES || e.interval_minutes > SCHEDULE_MAX_MINUTES
        || typeof e.desktop_toast !== "boolean" || !validScanShape(e.scan) || !e.site.trim() || !e.name.trim()) throw new Error(bad);
      const key = scheduleKey(e.site.trim(), e.name.trim());
      if (seen.has(key)) throw new Error(bad);
      seen.add(key);
      return { site: e.site.trim(), name: e.name.trim(), interval_minutes: e.interval_minutes, desktop_toast: e.desktop_toast, scan: e.scan };
    });
  }

  function readSchedules() {
    state.schedulesError = "";
    state.schedules = [];
    try {
      const raw = localStorage.getItem(SCHEDULES_KEY);
      if (raw === null) return;
      state.schedules = validateSchedulesShape(JSON.parse(raw));
    } catch (e) {
      state.schedulesError = errText(e);
      state.schedules = [];
    }
  }

  function writeSchedules(list) {
    const value = { version: 1, schedules: list };
    validateSchedulesShape(value);
    const serialized = JSON.stringify(value);
    localStorage.setItem(SCHEDULES_KEY, serialized);
    if (localStorage.getItem(SCHEDULES_KEY) !== serialized) throw new Error("Saved schedules could not be verified after writing.");
    state.schedules = list;
  }

  // The scan plan a schedule uses: what a manual scan would send now, without SNMP.
  function currentScanSettings() {
    const r = request(false);
    return { mode: r.mode, ports: r.ports, probes: { ...r.probes, snmp: false }, concurrency: r.concurrency, timeout_ms: r.timeout_ms };
  }

  const selectedTargetSet = () => {
    const option = $("target-set-list").selectedOptions[0];
    return option ? state.targetSets[Number(option.value)] : undefined;
  };
  const scheduleFor = (entry) => entry && state.schedules.find((s) => scheduleKey(s.site, s.name) === scheduleKey(entry.site, entry.name));
  const clockTime = (iso) => new Date(iso).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });

  function scheduleStatus(message) {
    $("schedule-status").textContent = message || "";
  }

  function scheduleDescription(entry) {
    const sched = scheduleFor(entry);
    if (!sched) return "";
    const st = state.scheduleStatuses.find((s) => s.key === scheduleKey(entry.site, entry.name));
    const parts = ["Plan: " + (sched.scan.mode === "gentle" ? "gentle mode" : "default mode") + ", every " + sched.interval_minutes + " minutes."];
    if (!st) return parts.join(" ");
    if (st.error) return "Not running: " + st.error;
    if (st.running) parts.push("Rescanning now.");
    else if (st.next_run) parts.push("Next rescan at about " + clockTime(st.next_run) + ".");
    if (st.last_run && st.last_result) parts.push("Last rescan " + clockTime(st.last_run) + ": " + st.last_result + ".");
    if (st.last_error) parts.push("Problem: " + st.last_error + ".");
    if (st.skipped) parts.push("Skipped " + st.skipped + " cycle" + (st.skipped === 1 ? "" : "s") + " because " + st.skip_reason + ".");
    return parts.join(" ");
  }

  // Shows the schedule of the selected saved set. entry is undefined when no set is selected.
  function fillScheduleForm(entry) {
    const sched = scheduleFor(entry);
    const usable = !!entry && !state.schedulesError;
    $("schedule-enabled").checked = !!sched;
    $("schedule-interval").value = String(sched ? sched.interval_minutes : 60);
    $("schedule-toast").checked = sched ? sched.desktop_toast : false;
    for (const id of ["schedule-enabled", "schedule-interval", "schedule-toast", "schedule-apply"]) $(id).disabled = !usable || state.scheduleBusy;
    $("schedule-reset").hidden = !state.schedulesError;
    const folder = (state.scheduleStatuses.find((s) => s.folder) || {}).folder;
    $("schedule-folder").hidden = !folder;
    $("schedule-folder").textContent = folder ? "Results folder: " + folder : "";
    scheduleStatus(state.schedulesError || (entry ? scheduleDescription(entry) : "Choose a saved set above to schedule it."));
  }

  function paintScheduleState() {
    const statuses = state.scheduleStatuses;
    const live = statuses.filter((s) => !s.error);
    const running = live.find((s) => s.running);
    const refused = statuses.length - live.length;
    const next = live.map((s) => s.next_run).filter(Boolean).sort()[0];
    let text = "";
    if (running) text = "Scheduled rescan running: " + running.site + " · " + running.name;
    else if (live.length) text = "Scheduled rescans on: " + live.length + (next ? " · next at about " + clockTime(next) : "");
    if (refused) text += (text ? " · " : "") + refused + " not running (see Target sets)";
    $("schedule-state").textContent = text;
    $("schedule-state").hidden = !text;
    $("schedule-stop").hidden = !running;
    if (!$("target-sets").hidden) fillScheduleForm(selectedTargetSet());
  }

  // Resolves each schedule against its saved set and hands the list to the app.
  async function pushSchedules() {
    if (!api || state.schedulesError || state.targetSetsError) return;
    const configs = [];
    const orphans = [];
    for (const sched of state.schedules) {
      const set = state.targetSets.find((s) => scheduleKey(s.site, s.name) === scheduleKey(sched.site, sched.name));
      if (set) configs.push({ ...sched, targets: set.targets });
      else orphans.push(sched);
    }
    if (orphans.length) {
      writeSchedules(state.schedules.filter((s) => !orphans.includes(s)));
      toast("Stopped the schedule for a saved set that no longer exists.");
    }
    state.scheduleStatuses = (await api.SetSchedules(configs)) || [];
    paintScheduleState();
  }

  async function applySchedule() {
    const entry = selectedTargetSet();
    if (!api || !entry || state.schedulesError || state.scheduleBusy) return;
    // Read the form before anything redraws it.
    const enabled = $("schedule-enabled").checked;
    const interval = Number($("schedule-interval").value);
    const toastOn = $("schedule-toast").checked;
    state.scheduleBusy = true;
    fillScheduleForm(entry);
    try {
      let next = state.schedules.filter((s) => scheduleKey(s.site, s.name) !== scheduleKey(entry.site, entry.name));
      if (enabled) {
        const sched = { site: entry.site, name: entry.name, interval_minutes: interval, desktop_toast: toastOn, scan: currentScanSettings() };
        await api.ValidateSchedule({ ...sched, targets: entry.targets });
        next = [...next, sched];
      }
      writeSchedules(next);
      await pushSchedules();
      state.scheduleBusy = false;
      fillScheduleForm(entry);
      scheduleStatus((enabled ? "Schedule saved. " : "Schedule turned off. ") + scheduleDescription(entry));
    } catch (e) {
      state.scheduleBusy = false;
      fillScheduleForm(entry);
      scheduleStatus("Not scheduled: " + errText(e));
    }
  }

  async function resetSchedules() {
    if (!state.schedulesError) return;
    if (!await confirmDialog("Reset saved schedules?", "This permanently removes the malformed saved schedule data from this app. Scheduled rescans stay off.", "Reset schedules")) return;
    try {
      localStorage.removeItem(SCHEDULES_KEY);
      if (localStorage.getItem(SCHEDULES_KEY) !== null) throw new Error("Saved schedules could not be removed from local storage.");
      state.schedules = [];
      state.schedulesError = "";
      if (api) state.scheduleStatuses = (await api.SetSchedules([])) || [];
      paintScheduleState();
      fillScheduleForm(selectedTargetSet());
      scheduleStatus("Saved schedules were reset.");
    } catch (e) { scheduleStatus(errText(e)); }
  }

  // New devices found by a scheduled rescan: an in-app banner (always), the window title, and, in the app, a
  // taskbar flash and an optional Windows notification. All of it is counts, labels you typed and addresses.
  function renderScheduleBanner() {
    const notices = [...state.scheduleNotices.values()];
    $("schedule-banner").hidden = !notices.length;
    $("schedule-banner-list").replaceChildren(...notices.map((n) => el("li", null,
      el("span", { text: n.title.replace(/^ZTG Scanner: /, "") + " on " + n.site + " · " + n.name + ": " + n.text }),
      el("button", { type: "button", class: "link", text: "Show changes", "data-key": n.key, onclick: () => showScheduleChanges(n.key) }))));
    const total = notices.reduce((sum, n) => sum + n.new_count, 0);
    if (rt && rt.WindowSetTitle) rt.WindowSetTitle(total ? document.title + ": " + total + " new device" + (total === 1 ? "" : "s") : document.title);
  }

  function onScheduleNotice(n) {
    if (!n || typeof n.key !== "string") return;
    state.scheduleNotices.set(n.key, n);
    renderScheduleBanner();
  }

  async function showScheduleChanges(key) {
    try { showComparison(await api.ScheduleChanges(key)); } catch (e) { toast(errText(e)); }
  }

  // ---------- Open with commands (SC10) ----------
  //
  // Commands are the technician's own: they live in this WebView's local storage (like saved target sets)
  // and are handed to the app, which validates them again and keeps the copy it launches from. A launch
  // sends only a command name and an address; the app takes the hostname from its own results. Nothing a
  // device reports is used here except as text.

  const OPEN_WITH_KEY = "ztg-scanner:open-with";
  const OPEN_WITH_MAX = 20;
  const errText = (e) => String(e && e.message ? e.message : e);

  function readOpenWithStore() {
    const raw = localStorage.getItem(OPEN_WITH_KEY);
    if (raw === null) return [];
    const value = JSON.parse(raw);
    if (!value || value.version !== 1 || !Array.isArray(value.commands) || value.commands.length > OPEN_WITH_MAX) throw new Error("Saved Open with commands are not in a recognized format.");
    return value.commands.map((c) => {
      if (!c || typeof c !== "object" || typeof c.name !== "string" || !["program", "url"].includes(c.kind)) throw new Error("Saved Open with commands are not in a recognized format.");
      const out = { name: c.name, kind: c.kind };
      if (c.kind === "program") {
        if (typeof c.program !== "string" || (c.args !== undefined && (!Array.isArray(c.args) || c.args.some((a) => typeof a !== "string")))) throw new Error("Saved Open with commands are not in a recognized format.");
        out.program = c.program;
        out.args = c.args || [];
      } else {
        if (typeof c.url !== "string") throw new Error("Saved Open with commands are not in a recognized format.");
        out.url = c.url;
      }
      return out;
    });
  }

  function writeOpenWithStore(commands) {
    const serialized = JSON.stringify({ version: 1, commands });
    localStorage.setItem(OPEN_WITH_KEY, serialized);
    if (localStorage.getItem(OPEN_WITH_KEY) !== serialized) throw new Error("Saved commands could not be verified after writing.");
  }

  // Load at start: an unreadable or invalid store leaves no commands active and says why.
  async function loadOpenWith() {
    const ow = state.openWith;
    ow.error = "";
    ow.commands = [];
    try {
      const stored = readOpenWithStore();
      ow.commands = await api.SetOpenWithCommands(stored);
    } catch (e) {
      ow.error = errText(e);
    }
  }

  function openWithStatus(message = "") {
    const ow = state.openWith;
    $("ow-status").textContent = ow.error && message !== ow.error ? ow.error + (message ? " " + message : "") : message;
    $("ow-reset").hidden = !ow.error;
  }

  function renderOpenWithList(selectName = "") {
    const list = $("ow-list");
    list.replaceChildren(...state.openWith.commands.map((c, i) => el("option", { value: String(i), text: c.name + " · " + (c.kind === "url" ? "web address" : "program") })));
    const i = state.openWith.commands.findIndex((c) => c.name === selectName);
    if (i >= 0) list.value = String(i);
  }

  function fillOpenWithForm(c) {
    state.openWith.editing = c ? state.openWith.commands.indexOf(c) : -1;
    $("ow-name").value = c ? c.name : "";
    $("ow-kind").value = c ? c.kind : "program";
    $("ow-program").value = c && c.kind === "program" ? c.program : "";
    $("ow-args").value = c && c.kind === "program" ? (c.args || []).join("\n") : "";
    $("ow-url").value = c && c.kind === "url" ? c.url : "";
    $("ow-preview").textContent = "";
    syncOpenWithKind();
  }

  function syncOpenWithKind() {
    const url = $("ow-kind").value === "url";
    $("ow-program-fields").hidden = url;
    $("ow-url-fields").hidden = !url;
  }

  function openWithFromForm() {
    const kind = $("ow-kind").value;
    const c = { name: $("ow-name").value, kind };
    if (kind === "url") c.url = $("ow-url").value;
    else {
      c.program = $("ow-program").value;
      const lines = $("ow-args").value.split(/\r?\n/);
      while (lines.length && lines[lines.length - 1] === "") lines.pop();
      c.args = lines;
    }
    return c;
  }

  function setOpenWithBusy(busy) {
    state.openWith.busy = busy;
    for (const id of ["ow-save", "ow-delete", "ow-new", "ow-preview-btn", "ow-reset", "ow-list"]) $(id).disabled = busy;
  }

  // Applies a new list: the app validates it first, then it is written and read back. If the write
  // fails, the app is put back on the previous list so what runs is always what is saved.
  async function applyOpenWithList(next, selectName) {
    const ow = state.openWith;
    const previous = ow.commands;
    const validated = await api.SetOpenWithCommands(next);
    try {
      writeOpenWithStore(validated);
    } catch (e) {
      await api.SetOpenWithCommands(previous).catch(() => {});
      throw e;
    }
    ow.commands = validated;
    ow.error = "";
    renderOpenWithList(selectName);
  }

  async function saveOpenWith() {
    const ow = state.openWith;
    if (!api || ow.busy || ow.error) return;
    setOpenWithBusy(true);
    try {
      const c = openWithFromForm();
      const key = c.name.trim().toLowerCase();
      let index = ow.editing;
      const clash = ow.commands.findIndex((x, i) => i !== index && x.name.toLowerCase() === key);
      if (clash >= 0) {
        if (!await confirmDialog("Replace command?", "A command named “" + ow.commands[clash].name + "” already exists. Replace it?", "Replace command")) return;
        index = clash;
      }
      const next = [...ow.commands];
      if (index >= 0) next[index] = c;
      else next.push(c);
      await applyOpenWithList(next, c.name.trim());
      fillOpenWithForm(ow.commands.find((x) => x.name === c.name.trim()));
      openWithStatus("Command saved.");
    } catch (e) {
      openWithStatus(errText(e));
    } finally { setOpenWithBusy(false); }
  }

  async function deleteOpenWith() {
    const ow = state.openWith;
    if (!api || ow.busy || ow.error) return;
    const entry = ow.commands[ow.editing];
    if (!entry) return openWithStatus("Choose a saved command to delete.");
    setOpenWithBusy(true);
    try {
      if (!await confirmDialog("Delete command?", "Delete “" + entry.name + "”?", "Delete command")) return;
      await applyOpenWithList(ow.commands.filter((x) => x !== entry), "");
      fillOpenWithForm(null);
      openWithStatus("Command deleted.");
    } catch (e) { openWithStatus(errText(e)); }
    finally { setOpenWithBusy(false); }
  }

  async function resetOpenWith() {
    const ow = state.openWith;
    if (!ow.error || ow.busy) return;
    setOpenWithBusy(true);
    try {
      if (!await confirmDialog("Reset saved commands?", "This permanently removes the unreadable saved Open with data from this app.", "Reset saved commands")) return;
      localStorage.removeItem(OPEN_WITH_KEY);
      if (localStorage.getItem(OPEN_WITH_KEY) !== null) throw new Error("Saved commands could not be removed from local storage.");
      ow.commands = await api.SetOpenWithCommands([]);
      ow.error = "";
      renderOpenWithList();
      fillOpenWithForm(null);
      openWithStatus("Saved commands were reset.");
    } catch (e) { openWithStatus(errText(e)); }
    finally { setOpenWithBusy(false); }
  }

  // Shows exactly what would run for the test values, as one quoted line per argument. Starts nothing.
  async function previewOpenWith() {
    if (!api || state.openWith.busy) return;
    const out = $("ow-preview");
    try {
      const r = await api.PreviewOpenWith(openWithFromForm(), $("ow-test-ip").value, $("ow-test-host").value);
      out.textContent = r.kind === "url"
        ? "Open in the default browser:\n" + r.url
        : "Program and arguments, one per line in brackets (no shell):\n" + (r.argv || []).map((a, i) => (i === 0 ? "program  [" : "arg " + i + "    [") + a + "]").join("\n");
    } catch (e) {
      out.textContent = "Not usable: " + errText(e);
    }
  }

  function openOpenWith() {
    if (!$("open-with").hidden) return;
    state.openWith.returnFocus = document.activeElement;
    closeMenus();
    for (const id of ["settings", "target-sets", "comparison", "diagnostics"]) $(id).hidden = true;
    $("settings-btn").setAttribute("aria-expanded", "false");
    $("target-sets-btn").setAttribute("aria-expanded", "false");
    renderOpenWithList();
    fillOpenWithForm(null);
    openWithStatus(state.openWith.commands.length ? "" : "No commands yet. Add PuTTY, mstsc or an RMM console link below.");
    $("open-with").hidden = false;
    $("ow-name").focus();
  }

  function closeOpenWith() {
    if ($("open-with").hidden) return;
    $("open-with").hidden = true;
    const back = state.openWith.returnFocus;
    if (back && back.focus && back.isConnected) back.focus();
    state.openWith.returnFocus = null;
  }

  // The row menu: built from the app's answer for this device, so a command that needs a hostname the
  // device does not have is shown as unavailable with the reason.
  async function showOpenWithMenu(ip, x, y) {
    const menu = $("openwith-menu");
    closeMenus();
    menu.replaceChildren(el("li", { class: "muted", text: "Loading…" }));
    menu.hidden = false;
    placeMenu(menu, x, y);
    let options;
    try { options = await api.OpenWithOptions(ip); }
    catch (e) { menu.hidden = true; return toast(errText(e)); }
    const items = options.map((o) => el("li", { role: "none" }, el("button", {
      type: "button", role: "menuitem", "data-command": o.name, disabled: !o.available, title: o.available ? "" : o.reason,
      onclick: () => { closeMenus(); runOpenWith(o.name, ip); },
    }, o.name, el("span", { class: "sub", text: o.available ? (o.kind === "url" ? "Web address" : "Program") : o.reason }))));
    if (state.openWith.error) items.unshift(el("li", { class: "muted", text: "Saved commands could not be loaded. See Open with settings." }));
    else if (!items.length) items.push(el("li", { class: "muted", text: "No commands yet" }));
    items.push(el("li", { role: "separator", class: "menu-sep" }),
      el("li", { role: "none" }, el("button", { type: "button", role: "menuitem", id: "openwith-manage", text: "Edit commands…", onclick: () => { closeMenus(); openOpenWith(); } })));
    menu.replaceChildren(...items);
    placeMenu(menu, x, y);
    const first = menu.querySelector("button:not(:disabled)");
    if (first) first.focus();
  }

  // Set through CSSOM: the Content-Security-Policy blocks inline style attributes.
  function placeMenu(menu, x, y) {
    const r = menu.getBoundingClientRect();
    menu.style.left = Math.max(8, Math.min(x, window.innerWidth - r.width - 8)) + "px";
    menu.style.top = Math.max(8, Math.min(y, window.innerHeight - r.height - 8)) + "px";
  }

  async function runOpenWith(name, ip, allowPublic = false) {
    try {
      toast(await api.OpenWith(name, ip, allowPublic));
    } catch (e) {
      const message = errText(e);
      if (!allowPublic && message.includes("public address space")) {
        if (await confirmDialog("Connect to a public address?", "Only continue if you are authorized to use " + name + " with " + ip + ".", "I am authorized")) return runOpenWith(name, ip, true);
        return;
      }
      toast(message, 6000);
    }
  }

  function showComparison(result) {
    state.comparison = result;
    $("comparison").hidden = false;
    $("open-with").hidden = true;
    $("diagnostics").hidden = true;
    renderComparison();
  }

  function renderComparison() {
    const result = state.comparison;
    if (!result) return;
    const labels = { new: "New", missing: "Missing", changed: "Changed", unchanged: "Unchanged", not_scanned: "Not assessed" };
    $("comparison-source").textContent = (/^Previous rescan of /.test(result.path) ? "" : "Baseline: ") + result.path;
    const changes = result.changes || [];
    $("comparison-counts").textContent = Object.entries(labels).map(([k, label]) => changes.filter(c => c.kind === k).length + " " + label.toLowerCase()).join(" · ");
    const filter = $("change-filter").value;
    const shown = changes.filter(c => filter === "changes" ? c.kind !== "unchanged" : c.kind === filter);
    $("comparison-list").replaceChildren(...shown.slice(0, 500).map(c => {
      const h = c.after || c.before;
      return el("li", { "data-change": c.kind }, el("strong", { text: labels[c.kind] + ": " + h.ip + (h.name ? " · " + h.name : "") }),
        c.before && c.after && c.before.ip !== c.after.ip ? el("p", { text: "Previous address: " + c.before.ip }) : null,
        (c.fields || []).map(field => {
          const value = host => {
            if (field === "services") return serviceSummary(host) || "none";
            if (field === "llmnr_name") return llmnrName(host) || "unknown";
            if (field === "wsd_types") return (host.wsd && (host.wsd.types || []).slice().sort().join(" ")) || "unknown";
            if (field === "smb_dialect") return (host.smb && host.smb.dialect) || "unknown";
            if (field === "smb_signing") return signingOf(host.smb) || "unknown";
            if (field === "favicon") return faviconSummary(host) || "unknown";
            if (field === "ipv6") return (host.ipv6 || []).filter((a) => /^fe[89ab]/i.test(a)).map((a) => a.replace(/%.*/, "")).sort().join(" ") || "unknown";
            if (["model", "serial", "firmware", "software"].includes(field)) {
              const key = field === "firmware" ? "firmware_rev" : field === "software" ? "software_rev" : field;
              return (host.snmp && host.snmp[key]) || "unknown";
            }
            return host[field] || "unknown";
          };
          return el("p", { class: "muted small", text: field + ": " + value(c.before) + " → " + value(c.after) });
        }),
        c.after ? el("button", { type: "button", class: "link", text: "Device details", onclick: () => { $("comparison").hidden = true; select(c.after.ip); } }) : null);
    }));
    $("comparison-limit").textContent = shown.length > 500 ? "Showing the first 500 of " + shown.length + ". Choose a change type to narrow the list." : shown.length ? "" : "No devices in this category.";
  }

  // ---------- scanning ----------

  function setRunning(running) {
    state.running = running;
    const b = $("scan-btn");
    b.textContent = running ? "Stop" : "Scan";
    b.classList.toggle("stop", running);
    $("targets").disabled = running;
    for (const id of ["site", "save-scan", "load-scan", "compare-scan", "new-site"]) $(id).disabled = running;
    updateTargetSetControls();
    renderDetail();
  }

  function setProgress(done, total, up) {
    const pct = total ? Math.round((done / total) * 100) : 0;
    $("progress-bar").style.width = pct + "%";
    document.querySelector(".progress").setAttribute("aria-valuenow", String(pct));
    const secs = Math.round((Date.now() - state.startedAt) / 1000);
    $("status-text").textContent = "Scanned " + done + " of " + total + " · " + up + " up · " + secs + "s";
  }

  function effectiveGentleProbes() {
    const g = (state.defaults && state.defaults.gentle && state.defaults.gentle.probes) || {};
    const out = {};
    for (const k of Object.keys(state.settings.probes)) out[k] = !!state.settings.probes[k] && !!g[k] && k !== "snmp";
    out.snmp = false;
    return out;
  }

  function request(allowPublic) {
    const s = state.settings;
    const gentle = s.mode === "gentle";
    // Gentle mode fixes the engine settings; the UI sends zeros so the plan
    // comes from the shared definition in the engine, not from this form.
    // Probes are masked to the gentle set and SNMP is always off.
    const probes = gentle ? effectiveGentleProbes() : s.probes;
    return {
      mode: gentle ? "gentle" : "default",
      targets: $("targets").value,
      ports: gentle ? "" : s.ports,
      probes,
      concurrency: gentle ? 0 : Number(s.concurrency) || 0,
      timeout_ms: gentle ? 0 : Number(s.timeout_ms) || 0,
      allow_public: !!allowPublic,
      snmp: !gentle && s.probes.snmp
        ? {
            username: $("snmp-user").value.trim(),
            auth_protocol: $("snmp-auth").value,
            auth_passphrase: $("snmp-auth-pass").value,
            priv_protocol: $("snmp-priv").value,
            priv_passphrase: $("snmp-priv-pass").value,
          }
        : null,
    };
  }

  async function startScan(allowPublic = false) {
    if (state.targetSetsBusy) return;
    if (!api) return toast("Not connected to the scanner engine.");
    store.set("targets", $("targets").value);
    try {
      await api.SetSite($("site").value);
      state.hosts.clear();
      select(null);
      $("warnings").textContent = "";
      state.startedAt = Date.now();
      setRunning(true);
      render();
      const n = await api.StartScan(request(allowPublic));
      state.comparison = null;
      $("comparison").hidden = true;
      markDirty();
      setProgress(0, n, 0);
    } catch (e) {
      setRunning(false);
      const msg = String(e && e.message ? e.message : e);
      await reload($("show-down").checked);
      render();
      if (!allowPublic && msg.includes("public address space")) {
        const ok = await confirmDialog(
          "Scan public addresses?",
          msg.replace(/;.*$/, "") + ". Only continue if you own this range or have written authorization from its owner to scan it.",
          "I am authorized, scan",
        );
        if (ok) return startScan(true);
        $("status-text").textContent = "Canceled: public addresses were not scanned.";
        return;
      }
      $("status-text").textContent = msg;
    }
  }

  function onHost(h) {
    state.hosts.set(h.ip, h);
    if (state.selected === h.ip) renderDetail();
    queueRender();
  }

  async function onDone(sum) {
    setRunning(false);
    const secs = sum.finished && sum.started ? ((new Date(sum.finished) - new Date(sum.started)) / 1000).toFixed(1) : "?";
    $("progress-bar").style.width = "100%";
    $("status-text").textContent = (sum.canceled ? "Stopped: " : "Done: ") + sum.up + " of " + sum.total + " up in " + secs + "s · " + (sum.methods || []).join(", ");
    $("warnings").textContent = (sum.warnings || []).join(" ");
    await reload($("show-down").checked);
    render();
  }

  async function reload(all) {
    if (!api) return;
    const snap = await api.Results(all);
    state.hosts.clear();
    for (const h of snap.hosts || []) state.hosts.set(h.ip, h);
    render();
    renderDetail();
  }

  // ---------- settings ----------

  function loadSettings() {
    const d = state.defaults;
    const saved = store.get("settings", null);
    state.settings = saved && saved.probes ? saved : { ports: d.ports, probes: { ...d.probes }, concurrency: d.concurrency, timeout_ms: d.timeout_ms };
    // Settings saved by an earlier version have no entry for probes added since;
    // those take the current default, and a probe the user switched off stays off.
    for (const k of Object.keys(d.probes)) if (!(k in state.settings.probes)) state.settings.probes[k] = d.probes[k];
    state.settings.probes.snmp = false; // opt-in for each session
    state.settings.mode = state.settings.mode === "gentle" ? "gentle" : "default";
    const fill = (sel, opts, cur) => {
      sel.replaceChildren(...opts.map((o) => el("option", { value: o, text: o })));
      sel.value = opts.includes(cur) ? cur : opts[0];
    };
    const snmp = state.settings.snmp || {};
    fill($("snmp-auth"), d.snmp_auth_protocols || [], snmp.auth_protocol || "SHA-256");
    fill($("snmp-priv"), d.snmp_priv_protocols || [], snmp.priv_protocol || "AES");
    $("snmp-user").value = snmp.username || "";
    paintSettings();
  }

  function paintSettings() {
    const s = state.settings;
    $("ports").value = s.ports;
    $("concurrency").value = s.concurrency;
    $("timeout").value = s.timeout_ms;
    $("snmp-fields").classList.toggle("on", !!s.probes.snmp);
    paintMode();
  }

  // paintMode shows the gentle settings and tradeoff from the engine's own
  // definition and locks the fields that gentle mode fixes.
  function paintMode() {
    const s = state.settings;
    const gentle = s.mode === "gentle";
    $("mode-default").checked = !gentle;
    $("mode-gentle").checked = gentle;
    const g = state.defaults.gentle;
    $("gentle-info").hidden = !gentle || !g;
    if (gentle && g) {
      $("gentle-summary").textContent = "Settings: " + g.concurrency + " hosts at a time, " + g.port_concurrency +
        " connections per host, at most " + g.probes_per_second + " probe starts per second in total, " +
        g.timeout_ms + " ms connect timeout, TCP ports " + g.ports.join(", ") +
        ". Service banners, NetBIOS, mDNS, UPnP, WS-Discovery, LLMNR, SMB, favicon and SNMP probes and the IPv6 multicast echo are skipped.";
      $("gentle-tradeoff").textContent = g.tradeoff;
    }
    // Show the probes that will really run, not stale checkboxes.
    const shown = gentle ? effectiveGentleProbes() : s.probes;
    for (const cb of document.querySelectorAll("[data-probe]")) cb.checked = !!shown[cb.dataset.probe];
    $("snmp-fields").classList.toggle("on", !!shown.snmp);
    for (const id of ["probe-fields", "snmp-fields", "engine-fields"]) $(id).disabled = gentle;
  }

  function readSettings() {
    const s = state.settings;
    // The boxes show the gentle probe set while gentle is on; only read them
    // when they reflect the user's own choices.
    const wasGentle = s.mode === "gentle";
    s.mode = $("mode-gentle").checked ? "gentle" : "default";
    if (!wasGentle && s.mode === "default") for (const cb of document.querySelectorAll("[data-probe]")) s.probes[cb.dataset.probe] = cb.checked;
    s.ports = $("ports").value.trim();
    s.concurrency = Number($("concurrency").value) || state.defaults.concurrency;
    s.timeout_ms = Number($("timeout").value) || state.defaults.timeout_ms;
    s.snmp = { username: $("snmp-user").value.trim(), auth_protocol: $("snmp-auth").value, priv_protocol: $("snmp-priv").value };
    $("snmp-fields").classList.toggle("on", !!s.probes.snmp);
    paintMode();
    // Saved: the username and protocols. Never saved: the passphrases, and
    // whether SNMP is on (it is opt-in each session).
    store.set("settings", { ...s, probes: { ...s.probes, snmp: false } });
  }

  function toggle(btn, panel, open) {
    const show = open === undefined ? panel.hidden : open;
    panel.hidden = !show;
    btn.setAttribute("aria-expanded", String(show));
  }

  function closeMenus() {
    toggle($("suggest-btn"), $("suggest-menu"), false);
    toggle($("export-btn"), $("export-menu"), false);
    toggle($("columns-btn"), $("columns-menu"), false);
    $("openwith-menu").hidden = true;
  }

  // ---------- wiring ----------

  async function exportView() {
    const cols = visibleCols();
    const headers = cols.map((c) => c.label || c.title);
    const rows = visibleHosts().map((h) => cols.map((c) => c.text(h)));
    if (!rows.length) return toast("There are no rows in this view to export.");
    try {
      const path = await api.ExportTable(headers, rows);
      if (path) toast("Saved " + path);
    } catch (err) {
      toast(String(err && err.message ? err.message : err));
    }
  }

  async function init() {
    initTitlebar();
    $("theme").addEventListener("change", () => store.set("theme", applyTheme($("theme").value)));
    state.layout = loadLayout();
    renderHead();
    renderColumnMenu();

    const head = $("grid").querySelector("thead");
    head.addEventListener("click", (e) => {
      if (e.target.closest(".resize") || Date.now() - state.resizedAt < 250) return;
      const th = e.target.closest("th[data-sort]");
      if (!th) return;
      if (state.sortKey === th.dataset.sort) state.sortDir = -state.sortDir;
      else (state.sortKey = th.dataset.sort), (state.sortDir = 1);
      render();
    });
    head.addEventListener("pointerdown", (e) => {
      const handle = e.target.closest(".resize");
      if (!handle || e.button !== 0) return;
      e.preventDefault();
      startResize(e, handle);
    });
    head.addEventListener("dblclick", (e) => {
      const handle = e.target.closest(".resize");
      if (handle) autoFit(handle.closest("th").dataset.col);
    });

    $("columns-btn").addEventListener("click", (e) => {
      e.stopPropagation();
      const open = $("columns-menu").hidden;
      closeMenus();
      toggle($("columns-btn"), $("columns-menu"), open);
    });
    $("columns-menu").addEventListener("change", (e) => {
      const box = e.target.closest("input[data-col]");
      if (!box) return;
      toggleColumn(box.dataset.col, box.checked);
      renderColumnMenu();
    });
    $("columns-menu").addEventListener("click", (e) => {
      if (e.target.id === "columns-reset") {
        state.layout = defaultLayout();
        saveLayout();
        renderHead();
        renderColumnMenu();
        render();
        toast("Columns restored to defaults");
      } else if (e.target.id === "columns-fit") {
        for (const c of visibleCols()) if (!c.fixed) autoFit(c.key);
      }
    });
    $("rows").addEventListener("click", (e) => {
      const tr = e.target.closest("tr[data-ip]");
      if (tr) select(tr.dataset.ip);
    });
    $("rows").addEventListener("keydown", (e) => {
      const tr = e.target.closest("tr[data-ip]");
      if (!tr) return;
      if (e.key === "Enter") select(tr.dataset.ip);
      if (e.key === "ArrowDown" && tr.nextElementSibling) tr.nextElementSibling.focus();
      if (e.key === "ArrowUp" && tr.previousElementSibling) tr.previousElementSibling.focus();
    });
    for (const id of ["filter", "type-filter", "findings-only", "favorites-only"]) $(id).addEventListener("input", render);
    $("site").addEventListener("input", () => markDirty());
    $("open-with-edit").addEventListener("click", openOpenWith);
    $("open-with-close").addEventListener("click", closeOpenWith);
    $("ow-list").addEventListener("change", () => {
      const option = $("ow-list").selectedOptions[0];
      fillOpenWithForm(option && state.openWith.commands[Number(option.value)]);
      openWithStatus("");
    });
    $("ow-kind").addEventListener("change", syncOpenWithKind);
    $("ow-new").addEventListener("click", () => { $("ow-list").selectedIndex = -1; fillOpenWithForm(null); openWithStatus(""); });
    $("ow-save").addEventListener("click", saveOpenWith);
    $("ow-delete").addEventListener("click", deleteOpenWith);
    $("ow-reset").addEventListener("click", resetOpenWith);
    $("ow-preview-btn").addEventListener("click", previewOpenWith);
    $("rows").addEventListener("contextmenu", (e) => {
      const tr = e.target.closest("tr[data-ip]");
      if (!tr || !api) return;
      e.preventDefault();
      select(tr.dataset.ip);
      showOpenWithMenu(tr.dataset.ip, e.clientX, e.clientY);
    });
    $("target-sets-btn").addEventListener("click", openTargetSets);
    $("target-sets-close").addEventListener("click", closeTargetSets);
    $("target-set-list").addEventListener("change", () => {
      const option = $("target-set-list").selectedOptions[0];
      fillTargetSetDraft(option && state.targetSets[Number(option.value)]);
      fillScheduleForm(selectedTargetSet());
    });
    $("target-set-save").addEventListener("click", saveTargetSet);
    $("target-set-load").addEventListener("click", useTargetSetDraft);
    $("target-set-delete").addEventListener("click", deleteTargetSet);
    $("target-set-import").addEventListener("click", importTargetSetFile);
    $("target-set-reset").addEventListener("click", resetTargetSets);
    $("schedule-apply").addEventListener("click", applySchedule);
    $("schedule-reset").addEventListener("click", resetSchedules);
    $("schedule-stop").addEventListener("click", () => api && api.StopScheduled());
    $("schedule-banner-dismiss").addEventListener("click", () => { state.scheduleNotices.clear(); renderScheduleBanner(); });
    $("save-scan").addEventListener("click", async () => {
      try {
        const path = await api.SaveScan($("site").value);
        if (path) { state.file = path; markDirty(false); toast("Saved " + path); }
      } catch (e) { toast(String(e)); }
    });
    $("load-scan").addEventListener("click", () => replaceInventory("load"));
    $("new-site").addEventListener("click", () => replaceInventory("new"));
    $("compare-scan").addEventListener("click", async () => {
      try {
        const result = await api.CompareScan($("site").value);
        if (!result.path) return;
        showComparison(result);
      } catch (e) { toast(String(e)); }
    });
    $("change-filter").addEventListener("change", renderComparison);
    $("comparison-close").addEventListener("click", () => { $("comparison").hidden = true; });
    $("diagnostics-close").addEventListener("click", () => { $("diagnostics").hidden = true; });
    $("show-down").addEventListener("change", async () => {
      if ($("show-down").checked && !state.running) await reload(true);
      render();
    });

    $("scan-form").addEventListener("submit", (e) => {
      e.preventDefault();
      closeMenus();
      if (state.running) {
        api && api.StopScan();
        $("status-text").textContent = "Stopping…";
      } else if (!state.targetSetsBusy) startScan(false);
    });

    $("suggest-btn").addEventListener("click", (e) => {
      e.stopPropagation();
      toggle($("export-btn"), $("export-menu"), false);
      toggle($("suggest-btn"), $("suggest-menu"));
    });
    $("export-btn").addEventListener("click", (e) => {
      e.stopPropagation();
      toggle($("suggest-btn"), $("suggest-menu"), false);
      toggle($("export-btn"), $("export-menu"));
    });
    $("export-menu").addEventListener("click", async (e) => {
      const b = e.target.closest("button[data-export]");
      if (!b || !api) return;
      closeMenus();
      if (b.dataset.export === "view") return exportView();
      try {
        if (!state.running) await api.SetSite($("site").value);
        const path = await api.Export(b.dataset.export, b.dataset.export === "html" || $("show-down").checked);
        if (path) toast("Saved " + path);
      } catch (err) {
        toast(String(err && err.message ? err.message : err));
      }
    });
    document.addEventListener("click", (e) => {
      if (!e.target.closest(".menu")) closeMenus();
    });
    document.addEventListener("keydown", (e) => {
      if (e.key === "Escape") {
        closeMenus();
        if (!$("modal").hidden) $("modal-cancel").click();
        else if (!$("target-sets").hidden) closeTargetSets();
        else if (!$("open-with").hidden) closeOpenWith();
        else if (!$("comparison").hidden) $("comparison").hidden = true;
        else if (!$("diagnostics").hidden) $("diagnostics").hidden = true;
        else if (!$("settings").hidden) toggle($("settings-btn"), $("settings"), false);
        else select(null);
      }
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "f") {
        e.preventDefault();
        $("filter").focus();
      }
    });

    $("settings-btn").addEventListener("click", () => {
      if ($("settings").hidden) $("open-with").hidden = true;
      toggle($("settings-btn"), $("settings"));
    });
    $("settings-close").addEventListener("click", () => {
      readSettings();
      toggle($("settings-btn"), $("settings"), false);
    });
    $("settings").addEventListener("change", readSettings);
    $("settings-reset").addEventListener("click", () => {
      store.set("settings", null);
      loadSettings();
      toast("Settings restored to defaults");
    });

    if (!api || !rt) {
      $("status-text").textContent = "The scanner engine is not connected. Start ZTG Scanner from its application, not a browser.";
      $("scan-btn").disabled = true;
      return;
    }

    rt.EventsOn("scan:host", onHost);
    rt.EventsOn("scan:progress", (p) => setProgress(p.done, p.total, p.up));
    rt.EventsOn("scan:done", onDone);
    rt.EventsOn("schedule:status", (st) => { state.scheduleStatuses = st || []; paintScheduleState(); });
    rt.EventsOn("schedule:notice", onScheduleNotice);

    const [version, defaults, suggestions] = await Promise.all([api.Version(), api.Defaults(), api.Suggestions()]);
    document.title = "ZTG Scanner " + version;
    $("build-version").textContent = "v" + version;
    $("build-version").title = "Build v" + version;
    $("check-updates").addEventListener("click", () => showUpdateDialog({
      tool: "scanner", current: version, openExternal: (url) => rt.BrowserOpenURL(url),
    }));
    if (rt && rt.WindowSetTitle) rt.WindowSetTitle(document.title);
    state.defaults = defaults;
    loadSettings();
    await loadOpenWith();
    // Saved schedules start again with the app; the first rescan is one interval away, never at launch.
    readTargetSets();
    readSchedules();
    pushSchedules().catch((e) => toast("Scheduled rescans could not start: " + errText(e)));

    const menu = $("suggest-menu");
    if (!suggestions || !suggestions.length) {
      menu.append(el("li", { class: "muted", text: "No active IPv4 networks found" }));
    }
    for (const s of suggestions || []) {
      menu.append(el("li", { role: "none" }, el("button", {
        role: "menuitem",
        onclick: () => {
          $("targets").value = s.range;
          closeMenus();
          $("targets").focus();
        },
      }, s.range, el("span", { class: "sub", text: s.interface + " · this computer is " + s.address }))));
    }
    $("targets").value = store.get("targets", "") || (suggestions && suggestions[0] ? suggestions[0].range : "");

    const snap = await api.Results(false);
    $("site").value = snap.site || "";
    state.file = snap.file || "";
    markDirty(!!snap.dirty);
    for (const h of snap.hosts || []) state.hosts.set(h.ip, h);
    setRunning(!!snap.running);
    render();
  }

  init().catch((e) => {
    $("status-text").textContent = "Failed to start: " + e;
  });
})();
