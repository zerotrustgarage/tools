// Stand-in for the Wails bindings and runtime, injected by run.mjs before
// the page loads. It replays a small, varied network, including a device
// whose web page title is an HTML injection attempt.
(() => {
  const handlers = {};
  const emit = (name, data) => (handlers[name] || []).forEach((f) => f(data));
  const calls = (window.__calls = []);

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
  window.go = { main: { App: {
    Version: async () => "0.2.0-test",
    Defaults: async () => ({ ports: "22,80,443", probes: { ports: true, banners: true, reverse_dns: true, netbios: true, mdns: true, ssdp: true }, concurrency: 128, timeout_ms: 800, max_hosts: 65536, snmp_auth_protocols: ["SHA", "SHA-256", "SHA-512"], snmp_priv_protocols: ["AES", "AES-256"] }),
    Suggestions: async () => [{ interface: "Ethernet", address: "192.168.50.10", range: "192.168.50.0/24" }],
    Results: async (all) => ({ running: false, hosts: all ? hosts.concat([{ ip: "192.168.50.2", status: "down" }]) : hosts.slice(0, 0) }),
    StartScan: async (req) => {
      calls.push(["StartScan", req]);
      if (req.targets.includes("8.8.8.8") && !req.allow_public) {
        throw "8.8.8.8: 8.8.8.8 is public address space; scanning it needs --allow-public and authorization from its owner";
      }
      let i = 0;
      timer = setInterval(() => {
        if (i < hosts.length) {
          emit("scan:host", hosts[i]);
          emit("scan:progress", { done: (i + 1) * 50, total: 254, up: i + 1 });
          i++;
        } else {
          clearInterval(timer);
          emit("scan:progress", { done: 254, total: 254, up: hosts.length });
          emit("scan:done", { started: "2026-10-22T01:00:00Z", finished: "2026-10-22T01:00:21Z", total: 254, up: hosts.length, methods: ["icmp-win32", "arp-win32", "tcp-connect"] });
        }
      }, 30);
      return 254;
    },
    StopScan: async () => calls.push(["StopScan"]),
    Export: async (fmt, all) => { calls.push(["Export", fmt, all]); return "C:\\Users\\tech\\Documents\\ztg-scan." + fmt; },
    OpenWeb: async (ip, port) => calls.push(["OpenWeb", ip, port]),
  } } };
  window.runtime = {
    EventsOn: (name, f) => (handlers[name] = handlers[name] || []).push(f),
    ClipboardSetText: async (t) => calls.push(["Clipboard", t]),
  };
})();
