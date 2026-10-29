// Stand-in for the Wails bindings and runtime, injected by run.mjs before
// the page loads. It replays a small, varied network, including a device
// whose web page title is an HTML injection attempt.
(() => {
  const handlers = {};
  const emit = (name, data) => (handlers[name] || []).forEach((f) => f(data));
  const calls = (window.__calls = []);
  window.__emit = emit; // lets a test deliver a runtime event such as schedule:notice

  const hosts = [
    { ip: "192.168.50.1", status: "up", name: "gw.lab.lan", type: "Firewall", type_reason: "vendor fortinet", mac: "00:09:0f:aa:bb:01", vendor: "Fortinet, Inc.", evidence: ["icmp", "arp", "tcp-open"], rtt_ms: 0.6, ttl: 255, os_hint: "Network equipment or Solaris (initial TTL 255)", dns_name: "gw.lab.lan",
      snmp: { sys_name: "lab-fw-01", sys_descr: "FortiGate-60F v7.4.3", enterprise: "Fortinet", model: "FGT60F", serial: "FGT60FTK2309XXXX", sys_location: "MDF rack 1", interfaces: 12 },
      services: [{ port: 443, name: "https", product: "", tls: { version: "TLS 1.3", subject: "CN=FortiGate", issuer: "CN=FortiGate", self_signed: true, not_after: "2027-03-01T00:00:00Z", sha256: "ab" }, http: { status: 200, title: "FortiGate Login" } }] },
    { ip: "192.168.50.20", status: "up", name: "FRONTDESK-PC", type: "Windows host", type_reason: "Windows RPC (135) and SMB (445) open", mac: "00:1b:21:aa:bb:cc", vendor: "Intel Corporate", evidence: ["icmp", "arp"], rtt_ms: 1.2, ttl: 128,
      netbios: { computer: "FRONTDESK-PC", workgroup: "LAB", names: [{ name: "FRONTDESK-PC", suffix: 0, group: false }, { name: "LAB", suffix: 0, group: true }] },
      services: [{ port: 135, name: "msrpc" }, { port: 445, name: "smb" }, { port: 3389, name: "rdp" }] },
    { ip: "192.168.50.40", status: "up", name: "NVR", type: "IP camera / NVR", type_reason: "vendor hikvision", mac: "44:19:b6:01:02:03", vendor: "Hangzhou Hikvision Digital Technology Co.,Ltd.", evidence: ["arp", "tcp-open"],
      services: [{ port: 23, name: "telnet", banner: "login:" }, { port: 80, name: "http", http: { status: 200, title: '<img src=x onerror="window.__pwned=1">Camera' } }, { port: 554, name: "rtsp" }],
      findings: [{ severity: "high", code: "ndaa-889", message: "Hardware from a vendor named in NDAA section 889." }, { severity: "high", code: "telnet", message: "Telnet is open: administration in cleartext." }] },
    { ip: "192.168.50.77", status: "up", name: "Office-Printer.local", type: "Printer", type_reason: "advertises _ipp._tcp over mDNS", mac: "30:05:5c:11:22:33", vendor: "Brother Industries, LTD.", evidence: ["icmp", "arp"],
      mdns: { hostname: "Office-Printer.local", services: ["_http._tcp", "_ipp._tcp", "_printer._tcp"] }, services: [{ port: 80, name: "http", http: { status: 200, title: "Brother HL-L2350DW" } }, { port: 9100, name: "jetdirect" }] },
    { ip: "192.168.50.120", status: "up", name: "", type: "Private-MAC device (phone, laptop or VM)", type_reason: "randomized MAC and no open ports", mac: "da:a1:19:00:00:01", mac_randomized: true, evidence: ["arp"] },
  ];

  let timer = null;
  let hasResults = false;
  let currentHosts = structuredClone(hosts);
  let site = "";
  let saved = null;
  const snapshot = (all) => ({ running: false, site, targets: ["192.168.50.0/24"], hosts: hasResults ? structuredClone(all ? currentHosts.concat([{ ip: "192.168.50.2", status: "down" }]) : currentHosts) : [] });
  const validateTargets = (input) => {
    if (typeof input !== "string" || input.length > 65536) throw "target list exceeds the 64 KiB limit";
    const seen = new Set();
    const normalized = [];
    for (const rawLine of input.replace(/^\uFEFF/, "").split("\n")) {
      const line = rawLine.split("#", 1)[0];
      for (const token of line.split(/[;,\s]+/).filter(Boolean)) {
        if (!( /^(?:\d{1,3}\.){3}\d{1,3}(?:\/(?:[0-9]|[12][0-9]|3[0-2])|-(?:(?:\d{1,3}\.){3}\d{1,3}|\d{1,3}))?$/.test(token)
          || /^(?=.{1,253}$)(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)(?:\.(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?))*\.?$/.test(token))) {
          throw "target list contains an invalid target";
        }
        if (!seen.has(token)) { seen.add(token); normalized.push(token); }
      }
    }
    if (!normalized.length) throw "target list is empty";
    return normalized.join(", ");
  };
  // Stand-ins for the Open with bindings (SC10). The real validation, rendering and launching are
  // tested in Go (internal/openwith, app/openwith_test.go); this keeps only the shape the UI relies on.
  let openWith = [];
  const hostnameOK = (h) => /^(?=.{1,253}$)[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)*$/.test(h) && !/\.?\d+$/.test(h.split(".").pop() === h ? "" : h.split(".").pop());
  const templatesOf = (c) => (c.kind === "url" ? [c.url] : c.args || []);
  const usesHost = (c) => templatesOf(c).some((t) => t.includes("{hostname}"));
  const openWithRender = (c, ip, host) => {
    if (usesHost(c) && !hostnameOK(host)) throw "the device hostname is not a valid DNS name";
    const sub = (t) => t.replaceAll("{ip}", ip).replaceAll("{hostname}", host);
    return c.kind === "url" ? { kind: "url", url: sub(c.url) } : { kind: "program", argv: [c.program, ...(c.args || []).map(sub)] };
  };
  // Stand-ins for the scheduled rescan bindings (SC7). The real scheduling, scope guard and comparison are
  // tested in Go (internal/schedule, app/schedule_test.go); this keeps the shape and the refusal text the UI relies on.
  let scheduleStatuses = [];
  const scheduleCheck = (c) => {
    if (!Number.isInteger(c.interval_minutes) || c.interval_minutes < 15 || c.interval_minutes > 1440) throw "the interval must be between 15 minutes and 24 hours";
    for (const spec of c.targets.split(/[\s,;]+/).filter(Boolean)) {
      if (!/^\d+\.\d+\.\d+\.\d+/.test(spec)) throw spec + " is a host name. Scheduled rescans take addresses, ranges and CIDR blocks only, because a name can point somewhere else later";
      const [a, b] = spec.split(/[./-]/).map(Number);
      const priv = a === 10 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168) || (a === 169 && b === 254) || (a === 127) || (a === 100 && b >= 64 && b <= 127);
      if (!priv) throw "scheduled rescans cover private and link-local addresses only: " + spec + " includes public address " + spec.split("/")[0] + ". A scan of public addresses needs fresh authorization each time, so use Scan for it";
    }
  };
  window.go = { main: { App: {
    ValidateSchedule: async (c) => { calls.push(["ValidateSchedule", structuredClone(c)]); scheduleCheck(c); },
    SetSchedules: async (list) => {
      calls.push(["SetSchedules", structuredClone(list)]);
      scheduleStatuses = list.map((c) => {
        const key = c.site.toLowerCase() + "\u0000" + c.name.toLowerCase();
        try { scheduleCheck(c); return { key, site: c.site, name: c.name, interval_minutes: c.interval_minutes, running: false, skipped: 0, next_run: new Date(Date.now() + c.interval_minutes * 60000).toISOString(), folder: "C:\\Users\\tech\\AppData\\Local\\ZTG Scanner\\scheduled" }; }
        catch (e) { return { key, site: c.site, name: c.name, interval_minutes: c.interval_minutes, error: String(e), running: false, skipped: 0 }; }
      });
      return structuredClone(scheduleStatuses);
    },
    ScheduleStatuses: async () => structuredClone(scheduleStatuses),
    ScheduleChanges: async (key) => { calls.push(["ScheduleChanges", key]); return { path: "Previous rescan of Clinic · Main LAN (2026-10-29 09:00)", changes: [{ kind: "new", after: { ip: "10.0.0.50", status: "up", name: '<img src=x onerror="window.__pwned=1">' } }] }; },
    StopScheduled: async () => calls.push(["StopScheduled"]),
    SetOpenWithCommands: async (list) => {
      calls.push(["SetOpenWithCommands", structuredClone(list)]);
      if (window.__openWithRefuse) throw window.__openWithRefuse;
      for (const c of list) {
        if (!c.name || !c.name.trim()) throw "enter a name";
        if (c.kind === "program" && /\\(cmd|powershell)\.exe$/i.test(c.program)) throw c.program.split("\\").pop() + " is a shell or script host and cannot be used; choose the program itself";
      }
      if (list.length > 20) throw "you can save up to 20 commands";
      openWith = structuredClone(list).map((c) => ({ ...c, name: c.name.trim() }));
      return structuredClone(openWith);
    },
    PreviewOpenWith: async (c, ip, host) => { calls.push(["PreviewOpenWith", c, ip, host]); return openWithRender(c, ip, host); },
    OpenWithOptions: async (ip) => {
      calls.push(["OpenWithOptions", ip]);
      const h = currentHosts.find((x) => x.ip === ip);
      if (!h) throw "that address is not in the results";
      const host = h.dns_name || h.name || "";
      return openWith.map((c) => {
        try { openWithRender(c, ip, host); return { name: c.name, kind: c.kind, available: true }; }
        catch (e) { return { name: c.name, kind: c.kind, available: false, reason: String(e) }; }
      });
    },
    OpenWith: async (name, ip, allow) => { calls.push(["OpenWith", name, ip, allow]); if (!allow && ip === "8.8.8.8") throw "public address space needs authorization for this action"; return "Opened " + name + " for " + ip; },
    Version: async () => "0.2.0-test",
    Defaults: async () => ({ ports: "22,80,443", probes: { ports: true, banners: true, reverse_dns: true, netbios: true, mdns: true, ssdp: true }, concurrency: 128, timeout_ms: 800, max_hosts: 65536, snmp_auth_protocols: ["SHA", "SHA-256", "SHA-512"], snmp_priv_protocols: ["AES", "AES-256"],
      // Mirrors scan.GentleInfo() in internal/scan/plan.go.
      gentle: { mode: "gentle", label: "Gentle", concurrency: 4, port_concurrency: 2, probes_per_second: 10, timeout_ms: 1500, ports: [22, 80, 443, 445, 515, 631, 8080, 9100], probes: { ports: true, banners: false, reverse_dns: true, netbios: false, mdns: false, ssdp: false, snmp: false }, tradeoff: "Gentle mode lowers the load a scan puts on a network and skips the more intrusive probes. It is slower and identifies less: no service banners, NetBIOS, mDNS, UPnP or SNMP, and only a few ports. It reduces risk but does not remove it: any scan can upset a very fragile device, so get approval and watch the network." } }),
    Suggestions: async () => [{ interface: "Ethernet", address: "192.168.50.10", range: "192.168.50.0/24" }],
    Results: async (all) => snapshot(all),
    ValidateTargets: async (input) => {
      calls.push(["ValidateTargets", input]);
      if (window.__holdTargetValidation) await new Promise(resolve => { window.__releaseTargetValidation = resolve; });
      return validateTargets(input);
    },
    ImportTargets: async () => {
      calls.push(["ImportTargets"]);
      const outcome = window.__targetImport || { result: "" };
      if (outcome.error) throw outcome.error;
      return outcome.result || "";
    },
    SetSite: async (name) => { calls.push(["SetSite", name]); site = name; },
    SetAnnotation: async (ip, note) => { calls.push(["SetAnnotation", ip, note]); Object.assign(currentHosts.find(h => h.ip === ip), note); },
    SaveScan: async (name) => { site = name; saved = snapshot(true); window.__savedScan = saved; calls.push(["SaveScan", name]); return "C:\\Scans\\clinic.json"; },
    LoadScan: async () => { calls.push(["LoadScan"]); if (saved) { site = saved.site; currentHosts = structuredClone(saved.hosts.filter(h => h.status === "up")); hasResults = true; } return "C:\\Scans\\clinic.json"; },
    NewSite: async () => { calls.push(["NewSite"]); hasResults = false; site = ""; currentHosts = structuredClone(hosts); },
    CompareScan: async (name) => { calls.push(["CompareScan", name]); return { path: "C:\\Scans\\baseline.json", changes: [
      { kind: "new", after: currentHosts[0] },
      { kind: "changed", before: { ...currentHosts[1], ip: "192.168.50.19" }, after: currentHosts[1], fields: ["ip", "services"] },
      { kind: "missing", before: { ip: "192.168.50.99", name: '<img src=x onerror="window.__pwned=1">' } },
      { kind: "not_scanned", before: { ip: "192.168.60.1" } },
      { kind: "unchanged", before: currentHosts[3], after: currentHosts[3] },
    ] }; },
    DeviceAction: async (action, ip, allow) => { calls.push(["DeviceAction", action, ip, allow]); return "Reply from " + ip + ': <img src=x onerror="window.__pwned=1">'; },
    WakeOnLAN: async (ip) => calls.push(["WakeOnLAN", ip]),
    StartScan: async (req) => {
      calls.push(["StartScan", req]);
      if (req.targets.includes("8.8.8.8") && !req.allow_public) {
        throw "8.8.8.8: 8.8.8.8 is public address space; scanning it needs --allow-public and authorization from its owner";
      }
      hasResults = true;
      let i = 0;
      timer = setInterval(() => {
        if (i < currentHosts.length) {
          emit("scan:host", structuredClone(currentHosts[i]));
          emit("scan:progress", { done: (i + 1) * 50, total: 254, up: i + 1 });
          i++;
        } else {
          clearInterval(timer);
          emit("scan:progress", { done: 254, total: 254, up: hosts.length });
          emit("scan:done", { started: "2026-10-22T01:00:00Z", finished: "2026-10-22T01:00:21Z", total: 254, up: hosts.length, methods: ["icmp-win32", "arp-win32", "tcp-connect"] });
        }
      }, window.__scanInterval || 30);
      return 254;
    },
    StopScan: async () => calls.push(["StopScan"]),
    Export: async (fmt, all) => { calls.push(["Export", fmt, all]); return "C:\\Users\\tech\\Documents\\ztg-scan." + fmt; },
    OpenWeb: async (ip, port) => calls.push(["OpenWeb", ip, port]),
    ExportTable: async (headers, rows) => { calls.push(["ExportTable", headers, rows]); return "C:\\Users\\tech\\Documents\\ztg-scan-view.csv"; },
  } } };
  let maximised = false;
  window.runtime = {
    BrowserOpenURL: (url) => calls.push(["BrowserOpenURL", url]),
    WindowSetTitle: (t) => calls.push(["WindowSetTitle", t]),
    EventsOn: (name, f) => (handlers[name] = handlers[name] || []).push(f),
    ClipboardSetText: async (t) => calls.push(["Clipboard", t]),
    WindowMinimise: () => calls.push(["WindowMinimise"]),
    WindowToggleMaximise: async () => { maximised = !maximised; calls.push(["WindowToggleMaximise"]); },
    WindowIsMaximised: async () => maximised,
    Quit: () => calls.push(["Quit"]),
    WindowSetBackgroundColour: (r, g, b, a) => calls.push(["WindowSetBackgroundColour", r, g, b, a]),
    WindowSetDarkTheme: () => calls.push(["WindowSetDarkTheme"]),
    WindowSetLightTheme: () => calls.push(["WindowSetLightTheme"]),
    WindowSetSystemDefaultTheme: () => calls.push(["WindowSetSystemDefaultTheme"]),
  };
})();
