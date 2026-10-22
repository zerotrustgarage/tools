// ZTG Scanner desktop UI. Talks to the Go side through the Wails bindings
// (window.go.main.App) and runtime events. Every string that came from a
// scanned device is inserted with textContent, never as HTML.
"use strict";

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

  // "auto" follows the Windows setting; "light" and "dark" are the user's choice and persist.
  const THEMES = ["auto", "light", "dark"];
  function applyTheme(choice) {
    const theme = THEMES.includes(choice) ? choice : "auto";
    if (theme === "auto") document.documentElement.removeAttribute("data-theme");
    else document.documentElement.setAttribute("data-theme", theme);
    const btn = $("theme-btn");
    if (btn) {
      btn.dataset.choice = theme;
      const label = `Theme: ${theme} (click to change)`;
      btn.title = label;
      btn.setAttribute("aria-label", label);
    }
    return theme;
  }
  applyTheme(store.get("theme", "auto"));

  // The desktop window has no native frame (main_windows.go); these are its caption buttons.
  function initTitlebar() {
    if (!rt || !rt.WindowMinimise) return; // browser preview: the browser has its own frame
    document.body.classList.add("has-titlebar");
    $("titlebar").hidden = false;
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
    showState();
  }

  // ---------- derived fields ----------

  function ipNum(ip) {
    return ip.split(".").reduce((a, o) => a * 256 + Number(o), 0);
  }
  const sevRank = { high: 3, medium: 2, info: 1 };
  const ports = (h) => (h.services || []).map((s) => s.port);
  const vendorOf = (h) => (h.mac_randomized ? "(private MAC)" : h.vendor || "");
  const worst = (h) => Math.max(0, ...(h.findings || []).map((f) => sevRank[f.severity] || 0));

  const modelOf = (h) => (h.upnp && [h.upnp.model_name, h.upnp.model_number].filter(Boolean).join(" ")) || (h.snmp && h.snmp.model) || "";
  const serviceSummary = (h) =>
    (h.services || []).map((s) => s.port + "/" + s.name + (s.http && s.http.title ? ' "' + s.http.title + '"' : s.product ? " " + s.product : "")).join("; ");

  // One definition per grid column drives the header, the cells, sorting and the
  // "current view" CSV export. text() is the plain value; cell() overrides display.
  const COLUMNS = [
    { key: "status", label: "", title: "Status", width: 34, fixed: true, text: (h) => h.status, sort: (h) => (h.status === "up" ? 0 : 1),
      cell: (h) => el("span", { class: "dot " + (h.status === "up" ? "up" : ""), title: h.status }) },
    { key: "name", label: "Name", width: 190, text: (h) => h.name || "" },
    { key: "ip", label: "IP address", width: 132, mono: true, text: (h) => h.ip, sort: (h) => ipNum(h.ip) },
    { key: "type", label: "Type", width: 190, text: (h) => h.type || "", tip: (h) => h.type_reason },
    { key: "flags", label: "Findings", width: 160, sort: (h) => -worst(h), cell: findingBadges,
      text: (h) => (h.findings || []).map((f) => f.severity + ":" + f.code).join(" ") },
    { key: "vendor", label: "Manufacturer", width: 210, text: vendorOf },
    { key: "mac", label: "MAC address", width: 140, mono: true, text: (h) => h.mac || "" },
    { key: "ports", label: "Open ports", width: 150, mono: true, text: (h) => ports(h).join(" "), sort: (h) => ports(h).length },
    // Optional columns, hidden until chosen in the Columns menu.
    { key: "dns", label: "DNS name", width: 200, hidden: true, text: (h) => h.dns_name || "" },
    { key: "netbios", label: "NetBIOS name", width: 150, hidden: true, text: (h) => (h.netbios && h.netbios.computer) || "" },
    { key: "workgroup", label: "Workgroup", width: 120, hidden: true, text: (h) => (h.netbios && h.netbios.workgroup) || "" },
    { key: "mdns", label: "mDNS name", width: 180, hidden: true, text: (h) => (h.mdns && h.mdns.hostname) || "" },
    { key: "model", label: "Model", width: 170, hidden: true, text: modelOf },
    { key: "serial", label: "Serial", width: 150, hidden: true, mono: true, text: (h) => (h.snmp && h.snmp.serial) || "" },
    { key: "firmware", label: "Firmware", width: 130, hidden: true, text: (h) => (h.snmp && (h.snmp.firmware_rev || h.snmp.software_rev)) || "" },
    { key: "location", label: "Location", width: 150, hidden: true, text: (h) => (h.snmp && h.snmp.sys_location) || "" },
    { key: "os", label: "OS hint", width: 200, hidden: true, text: (h) => h.os_hint || "" },
    { key: "rtt", label: "Ping (ms)", width: 80, hidden: true, mono: true, text: (h) => (h.rtt_ms ? String(h.rtt_ms) : ""), sort: (h) => h.rtt_ms || 1e9 },
    { key: "ttl", label: "TTL", width: 60, hidden: true, mono: true, text: (h) => (h.ttl ? String(h.ttl) : ""), sort: (h) => h.ttl || 999 },
    { key: "services", label: "Services", width: 300, hidden: true, text: serviceSummary },
    { key: "evidence", label: "Found by", width: 160, hidden: true, text: (h) => (h.evidence || []).join(", ") },
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
      h.ip, h.name, h.mac, h.vendor, h.type, h.dns_name,
      h.netbios && h.netbios.computer, h.netbios && h.netbios.workgroup,
      h.mdns && h.mdns.hostname, h.upnp && h.upnp.manufacturer, h.upnp && h.upnp.model_name,
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
      .filter((h) => (showDown || h.status === "up") && (!type || h.type === type) && (!findingsOnly || (h.findings || []).length) && matches(h, q))
      .sort((a, b) => {
        const x = key(a), y = key(b);
        return (x < y ? -1 : x > y ? 1 : ipNum(a.ip) - ipNum(b.ip)) * state.sortDir;
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
        web && api ? el("button", { class: "link", text: "Open in browser", onclick: () => api.OpenWeb(h.ip, s.port).catch((e) => toast(String(e))) }) : null),
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
      ]),
    );
  }

  function renderDetail() {
    const pane = $("detail");
    const h = state.selected && state.hosts.get(state.selected);
    if (!h) {
      pane.hidden = true;
      return;
    }
    pane.hidden = false;
    const nb = h.netbios, md = h.mdns, up = h.upnp;
    pane.replaceChildren(
      el("button", { class: "ghost close", "aria-label": "Close details", text: "✕", onclick: () => select(null) }),
      el("h2", { text: h.name || h.ip }),
      el("div", { class: "muted mono" }, h.ip, " ", copyButton(h.ip)),
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
        ["Manufacturer", h.mac_randomized ? "Private (randomized) MAC address: no manufacturer" : h.vendor],
        ["UPnP maker", up && up.manufacturer],
        ["Model", (up && [up.model_name, up.model_number].filter(Boolean).join(" ")) || (h.snmp && h.snmp.model)],
        ["UPnP type", up && up.device_type],
      ])),
      ...section("Names", kv([
        ["DNS", h.dns_name],
        ["NetBIOS", nb && nb.computer],
        ["Workgroup", nb && nb.workgroup],
        ["mDNS", md && md.hostname],
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

  // ---------- scanning ----------

  function setRunning(running) {
    state.running = running;
    const b = $("scan-btn");
    b.textContent = running ? "Stop" : "Scan";
    b.classList.toggle("stop", running);
    $("targets").disabled = running;
  }

  function setProgress(done, total, up) {
    const pct = total ? Math.round((done / total) * 100) : 0;
    $("progress-bar").style.width = pct + "%";
    document.querySelector(".progress").setAttribute("aria-valuenow", String(pct));
    const secs = Math.round((Date.now() - state.startedAt) / 1000);
    $("status-text").textContent = "Scanned " + done + " of " + total + " · " + up + " up · " + secs + "s";
  }

  function request(allowPublic) {
    const s = state.settings;
    return {
      targets: $("targets").value,
      ports: s.ports,
      probes: s.probes,
      concurrency: Number(s.concurrency) || 0,
      timeout_ms: Number(s.timeout_ms) || 0,
      allow_public: !!allowPublic,
      snmp: s.probes.snmp
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
    if (!api) return toast("Not connected to the scanner engine.");
    store.set("targets", $("targets").value);
    try {
      state.hosts.clear();
      select(null);
      $("warnings").textContent = "";
      state.startedAt = Date.now();
      setRunning(true);
      render();
      const n = await api.StartScan(request(allowPublic));
      setProgress(0, n, 0);
    } catch (e) {
      setRunning(false);
      const msg = String(e && e.message ? e.message : e);
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
    if ($("show-down").checked) await reload(true);
    render();
  }

  async function reload(all) {
    if (!api) return;
    const snap = await api.Results(all);
    state.hosts.clear();
    for (const h of snap.hosts || []) state.hosts.set(h.ip, h);
    render();
  }

  // ---------- settings ----------

  function loadSettings() {
    const d = state.defaults;
    const saved = store.get("settings", null);
    state.settings = saved && saved.probes ? saved : { ports: d.ports, probes: { ...d.probes }, concurrency: d.concurrency, timeout_ms: d.timeout_ms };
    state.settings.probes.snmp = false; // opt-in for each session
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
    for (const cb of document.querySelectorAll("[data-probe]")) cb.checked = !!s.probes[cb.dataset.probe];
    $("ports").value = s.ports;
    $("concurrency").value = s.concurrency;
    $("timeout").value = s.timeout_ms;
    $("snmp-fields").classList.toggle("on", !!s.probes.snmp);
  }

  function readSettings() {
    const s = state.settings;
    for (const cb of document.querySelectorAll("[data-probe]")) s.probes[cb.dataset.probe] = cb.checked;
    s.ports = $("ports").value.trim();
    s.concurrency = Number($("concurrency").value) || state.defaults.concurrency;
    s.timeout_ms = Number($("timeout").value) || state.defaults.timeout_ms;
    s.snmp = { username: $("snmp-user").value.trim(), auth_protocol: $("snmp-auth").value, priv_protocol: $("snmp-priv").value };
    $("snmp-fields").classList.toggle("on", !!s.probes.snmp);
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
    $("theme-btn").addEventListener("click", () => {
      const current = $("theme-btn").dataset.choice || "auto";
      store.set("theme", applyTheme(THEMES[(THEMES.indexOf(current) + 1) % THEMES.length]));
    });
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
    for (const id of ["filter", "type-filter", "findings-only"]) $(id).addEventListener("input", render);
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
      } else startScan(false);
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
        const path = await api.Export(b.dataset.export, $("show-down").checked);
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
        if (!$("settings").hidden) toggle($("settings-btn"), $("settings"), false);
        else if (!$("modal").hidden) $("modal-cancel").click();
        else select(null);
      }
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "f") {
        e.preventDefault();
        $("filter").focus();
      }
    });

    $("settings-btn").addEventListener("click", () => toggle($("settings-btn"), $("settings")));
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

    const [version, defaults, suggestions] = await Promise.all([api.Version(), api.Defaults(), api.Suggestions()]);
    document.title = "ZTG Scanner " + version;
    state.defaults = defaults;
    loadSettings();

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
    for (const h of snap.hosts || []) state.hosts.set(h.ip, h);
    setRunning(!!snap.running);
    render();
  }

  init().catch((e) => {
    $("status-text").textContent = "Failed to start: " + e;
  });
})();
