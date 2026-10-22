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

  // ---------- derived fields ----------

  function ipNum(ip) {
    return ip.split(".").reduce((a, o) => a * 256 + Number(o), 0);
  }
  const sevRank = { high: 3, medium: 2, info: 1 };
  const ports = (h) => (h.services || []).map((s) => s.port);
  const vendorOf = (h) => (h.mac_randomized ? "(private MAC)" : h.vendor || "");
  const worst = (h) => Math.max(0, ...(h.findings || []).map((f) => sevRank[f.severity] || 0));

  const sorters = {
    status: (h) => (h.status === "up" ? 0 : 1),
    name: (h) => (h.name || "~").toLowerCase(),
    ip: (h) => ipNum(h.ip),
    type: (h) => h.type || "~",
    vendor: (h) => vendorOf(h).toLowerCase() || "~",
    mac: (h) => h.mac || "~",
    ports: (h) => ports(h).length,
    flags: (h) => -worst(h),
  };

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
    const key = sorters[state.sortKey];
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

  function row(h) {
    const tr = el("tr", { class: (h.status === "up" ? "" : "down ") + (state.selected === h.ip ? "selected" : ""), "data-ip": h.ip, tabindex: 0 },
      el("td", null, el("span", { class: "dot " + (h.status === "up" ? "up" : ""), title: h.status })),
      el("td", { title: h.name || "", text: h.name || "" }),
      el("td", { class: "mono", text: h.ip }),
      el("td", { title: h.type_reason || "", text: h.type || "" }),
      el("td", null, findingBadges(h)),
      el("td", { title: vendorOf(h), text: vendorOf(h) }),
      el("td", { class: "mono", text: h.mac || "" }),
      el("td", { class: "mono", title: ports(h).join(", "), text: ports(h).join(" ") }),
    );
    return tr;
  }

  function render() {
    state.renderQueued = false;
    const list = visibleHosts();
    $("rows").replaceChildren(...list.map(row));
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
  }

  // ---------- wiring ----------

  async function init() {
    document.querySelector("thead").addEventListener("click", (e) => {
      const th = e.target.closest("th[data-sort]");
      if (!th) return;
      if (state.sortKey === th.dataset.sort) state.sortDir = -state.sortDir;
      else (state.sortKey = th.dataset.sort), (state.sortDir = 1);
      render();
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
