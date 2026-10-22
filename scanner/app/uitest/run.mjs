// UI test for the desktop frontend, run in headless Chromium against the
// binding mock (mock.js). Usage: node uitest/run.mjs [screenshot-dir]
// Requires the playwright package (npm i -g playwright) and a Chromium.
import { createRequire } from "node:module";
import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
import { extname, join, normalize } from "node:path";
import { fileURLToPath } from "node:url";
import assert from "node:assert/strict";

// createRequire honors NODE_PATH, so a global playwright install works.
const { chromium } = createRequire(import.meta.url)("playwright");
const here = fileURLToPath(new URL(".", import.meta.url));
const root = join(here, "..", "frontend");
const shots = process.argv[2];
const types = { ".html": "text/html", ".css": "text/css", ".js": "text/javascript", ".png": "image/png" };

const server = createServer(async (req, res) => {
  const path = normalize(new URL(req.url, "http://x").pathname).replace(/^(\.\.[/\\])+/, "");
  try {
    const body = await readFile(join(root, path === "/" ? "index.html" : path));
    res.writeHead(200, { "content-type": types[extname(path)] || "text/html" });
    res.end(body);
  } catch {
    res.writeHead(404).end(); // /wails/*.js: the mock stands in for them
  }
}).listen(0, "127.0.0.1");
await new Promise((r) => server.once("listening", r));
const url = `http://127.0.0.1:${server.address().port}/`;

const browser = await chromium.launch(process.env.CHROMIUM ? { executablePath: process.env.CHROMIUM } : {});
let failed = false;
try {
  for (const scheme of ["dark", "light"]) {
    const page = await browser.newPage({ viewport: { width: 1320, height: 820 }, colorScheme: scheme });
    const errors = [];
    page.on("pageerror", (e) => errors.push(String(e)));
    // A Content-Security-Policy violation means the UI silently lost a style or script.
    page.on("console", (m) => m.type() === "error" && /Content Security Policy/i.test(m.text()) && errors.push(m.text()));
    await page.addInitScript({ path: join(here, "mock.js") });
    await page.goto(url);

    // Prefilled from the machine's networks.
    await page.waitForFunction(() => document.getElementById("targets").value === "192.168.50.0/24");
    assert.equal(await page.title(), "ZTG Scanner 0.2.0-test");

    // Scan streams rows in and finishes.
    await page.click("#scan-btn");
    await page.waitForSelector("#rows tr:nth-child(5)");
    await page.waitForFunction(() => document.getElementById("status-text").textContent.startsWith("Done:"));
    assert.equal(await page.locator("#rows tr").count(), 5);
    assert.match(await page.textContent("#status-text"), /5 of 254 up/);

    // Device-supplied HTML is shown as text, never executed or parsed.
    await page.click('#rows tr[data-ip="192.168.50.40"]');
    await page.waitForSelector("#detail:not([hidden])");
    assert.ok((await page.textContent("#detail")).includes('<img src=x onerror="window.__pwned=1">Camera'));
    assert.equal(await page.evaluate(() => window.__pwned), undefined);
    assert.equal(await page.locator("#detail img").count(), 0);
    assert.match(await page.textContent("#detail"), /NDAA section 889/);

    // Open in browser goes through the bound method with host and port.
    await page.click('#detail button:has-text("Open in browser")');
    assert.deepEqual((await page.evaluate(() => window.__calls)).find((c) => c[0] === "OpenWeb"), ["OpenWeb", "192.168.50.40", 80]);

    if (shots) await page.screenshot({ path: join(shots, `ui-${scheme}.png`) });

    // Filters.
    await page.fill("#filter", "printer");
    assert.equal(await page.locator("#rows tr").count(), 1);
    await page.fill("#filter", "");
    await page.check("#findings-only");
    assert.equal(await page.locator("#rows tr").count(), 1);
    await page.uncheck("#findings-only");
    await page.selectOption("#type-filter", "Windows host");
    assert.equal(await page.locator("#rows tr").count(), 1);
    await page.selectOption("#type-filter", "");

    // Sort by name, descending on second click.
    await page.click('th[data-sort="name"]');
    await page.click('th[data-sort="name"]');
    assert.equal(await page.getAttribute('th[data-sort="name"]', "aria-sort"), "descending");

    // Unresponsive hosts appear on request.
    await page.check("#show-down");
    await page.waitForSelector('#rows tr[data-ip="192.168.50.2"]');

    // Export.
    await page.click("#export-btn");
    await page.click('button[data-export="csv"]');
    await page.waitForSelector("#toast:not([hidden])");
    assert.match(await page.textContent("#toast"), /Saved .*ztg-scan\.csv/);

    // Public range needs explicit confirmation; cancel sends nothing more.
    await page.fill("#targets", "8.8.8.8");
    await page.click("#scan-btn");
    await page.waitForSelector("#modal:not([hidden])");
    await page.click("#modal-cancel");
    let starts = (await page.evaluate(() => window.__calls)).filter((c) => c[0] === "StartScan");
    assert.equal(starts.at(-1)[1].allow_public, false);
    await page.click("#scan-btn");
    await page.waitForSelector("#modal:not([hidden])");
    await page.click("#modal-ok");
    await page.waitForFunction(() => window.__calls.filter((c) => c[0] === "StartScan").at(-1)[1].allow_public === true);

    // Settings persist into the request.
    await page.waitForFunction(() => document.getElementById("status-text").textContent.startsWith("Done:"));
    await page.click("#settings-btn");
    await page.uncheck('[data-probe="mdns"]');
    await page.fill("#ports", "80,443");
    await page.click("#settings-close");
    await page.fill("#targets", "192.168.50.0/24");
    await page.click("#scan-btn");
    starts = (await page.evaluate(() => window.__calls)).filter((c) => c[0] === "StartScan");
    assert.equal(starts.at(-1)[1].probes.mdns, false);
    assert.equal(starts.at(-1)[1].ports, "80,443");

    // SNMPv3: off at launch, credentials sent with the scan, passphrases never stored.
    await page.waitForFunction(() => document.getElementById("status-text").textContent.startsWith("Done:"));
    await page.click("#settings-btn");
    assert.equal(await page.isChecked("#snmp-on"), false);
    assert.equal(await page.isVisible("#snmp-user"), false);
    await page.check("#snmp-on");
    await page.fill("#snmp-user", "noc-ro");
    await page.selectOption("#snmp-auth", "SHA-512");
    await page.fill("#snmp-auth-pass", "auth-secret-1");
    await page.fill("#snmp-priv-pass", "priv-secret-1");
    await page.click("#settings-close");
    await page.click("#scan-btn");
    starts = (await page.evaluate(() => window.__calls)).filter((c) => c[0] === "StartScan");
    const snmpReq = starts.at(-1)[1];
    assert.equal(snmpReq.probes.snmp, true);
    assert.deepEqual(snmpReq.snmp, { username: "noc-ro", auth_protocol: "SHA-512", auth_passphrase: "auth-secret-1", priv_protocol: "AES", priv_passphrase: "priv-secret-1" });
    const saved = await page.evaluate(() => JSON.stringify(localStorage));
    assert.ok(!saved.includes("secret"), "passphrase persisted: " + saved);
    assert.ok(saved.includes("noc-ro"), "username not remembered");
    await page.waitForFunction(() => document.getElementById("status-text").textContent.startsWith("Done:"));
    await page.click('#rows tr[data-ip="192.168.50.1"]');
    assert.match(await page.textContent("#detail"), /FGT60FTK2309XXXX/);
    assert.match(await page.textContent("#detail"), /MDF rack 1/);
    if (shots) await page.screenshot({ path: join(shots, `ui-snmp-${scheme}.png`) });

    // After a reload SNMP is off again and the passphrases are gone.
    await page.reload();
    await page.waitForFunction(() => document.getElementById("targets").value !== "");
    await page.click("#settings-btn");
    assert.equal(await page.isChecked("#snmp-on"), false);
    await page.check("#snmp-on");
    assert.equal(await page.inputValue("#snmp-user"), "noc-ro");
    assert.equal(await page.inputValue("#snmp-auth-pass"), "");

    // ---------- Columns ----------
    await page.uncheck("#snmp-on");
    await page.click("#settings-close");
    await page.click("#scan-btn");
    await page.waitForFunction(() => document.getElementById("status-text").textContent.startsWith("Done:"));
    await page.keyboard.press("Escape"); // no detail pane
    const thWidth = (key) => page.evaluate((k) => document.querySelector(`#grid th[data-col="${k}"]`).getBoundingClientRect().width, key);

    // The table is exactly as wide as its columns; it does not stretch to the window.
    const widths = await page.evaluate(() => ({
      grid: document.getElementById("grid").getBoundingClientRect().width,
      cols: [...document.querySelectorAll("#grid thead th")].reduce((n, th) => n + th.getBoundingClientRect().width, 0),
      wrap: document.querySelector(".grid-wrap").getBoundingClientRect().width,
    }));
    assert.ok(Math.abs(widths.grid - widths.cols) < 2, `grid ${widths.grid} vs columns ${widths.cols}`);
    assert.ok(widths.grid < widths.wrap - 50, `grid ${widths.grid} stretched to ${widths.wrap}`);

    // Drag the Name column's edge 80px to the right; sorting must not change.
    const sortBefore = await page.getAttribute('th[data-sort="name"]', "aria-sort");
    const w0 = await thWidth("name");
    const handle = await page.locator('#grid th[data-col="name"] .resize').boundingBox();
    await page.mouse.move(handle.x + handle.width / 2, handle.y + handle.height / 2);
    await page.mouse.down();
    await page.mouse.move(handle.x + handle.width / 2 + 40, handle.y + handle.height / 2, { steps: 4 });
    await page.mouse.move(handle.x + handle.width / 2 + 80, handle.y + handle.height / 2, { steps: 4 });
    await page.mouse.up();
    const w1 = await thWidth("name");
    assert.ok(Math.abs(w1 - (w0 + 80)) <= 3, `name width ${w0} -> ${w1}, want +80`);
    assert.equal(await page.getAttribute('th[data-sort="name"]', "aria-sort"), sortBefore);

    // Double-click the edge: fit to the widest name (shorter than the dragged width).
    await page.dblclick('#grid th[data-col="name"] .resize');
    const w2 = await thWidth("name");
    assert.ok(w2 < w1 && w2 >= 100, `fit width ${w2} (dragged ${w1})`);
    const names = await page.evaluate(() => [...document.querySelectorAll('#rows td[data-col="name"]')].every((td) => td.scrollWidth <= td.clientWidth));
    assert.ok(names, "a name is clipped after fitting");

    // Column chooser: hide MAC, show Serial.
    await page.click("#columns-btn");
    await page.uncheck('#columns-menu input[data-col="mac"]');
    await page.check('#columns-menu input[data-col="serial"]');
    await page.keyboard.press("Escape");
    assert.equal(await page.locator('#grid th[data-col="mac"]').count(), 0);
    assert.equal(await page.locator('#rows td[data-col="mac"]').count(), 0);
    assert.equal((await page.textContent('#rows tr[data-ip="192.168.50.1"] td[data-col="serial"]')).trim(), "FGT60FTK2309XXXX");

    // Export this view: exactly the visible columns and the filtered rows, in order.
    await page.fill("#filter", "192.168.50.");
    await page.selectOption("#type-filter", "Firewall");
    await page.click("#export-btn");
    await page.click('button[data-export="view"]');
    await page.waitForFunction(() => window.__calls.some((c) => c[0] === "ExportTable"));
    const [, headers, rows] = (await page.evaluate(() => window.__calls)).filter((c) => c[0] === "ExportTable").at(-1);
    const shownHeaders = await page.$$eval("#grid thead th", (ths) => ths.map((th) => th.dataset.col));
    assert.equal(headers.length, shownHeaders.length);
    assert.ok(headers.includes("Serial") && !headers.includes("MAC address"), "headers " + headers);
    assert.equal(rows.length, 1);
    assert.equal(rows[0][headers.indexOf("IP address")], "192.168.50.1");
    assert.equal(rows[0][headers.indexOf("Serial")], "FGT60FTK2309XXXX");
    await page.fill("#filter", "");
    await page.selectOption("#type-filter", "");
    if (shots) await page.screenshot({ path: join(shots, `ui-columns-${scheme}.png`) });

    // The layout survives a restart.
    await page.reload();
    await page.waitForSelector('#grid th[data-col="serial"]');
    assert.equal(await page.locator('#grid th[data-col="mac"]').count(), 0);
    const restored = await thWidth("name");
    assert.ok(Math.abs(restored - w2) <= 1, `name width not restored: ${restored} vs ${w2}; saved ${await page.evaluate(() => localStorage.getItem("ztg-scanner:layout"))}`);

    // Reset columns restores the defaults.
    await page.click("#columns-btn");
    await page.click("#columns-reset");
    assert.equal(await page.locator('#grid th[data-col="mac"]').count(), 1);
    assert.equal(await page.locator('#grid th[data-col="serial"]').count(), 0);
    assert.equal(Math.round(await thWidth("name")), 190);

    // ---------- Title bar and theme ----------
    // The desktop runtime is present (mock), so the app draws its own title bar.
    assert.equal(await page.isVisible("#titlebar"), true);
    assert.equal(await page.isVisible(".topbar .brand"), false, "the toolbar repeats the product name");
    assert.equal(await page.evaluate(() => getComputedStyle(document.getElementById("titlebar")).getPropertyValue("--wails-draggable").trim()), "drag");
    assert.equal(await page.evaluate(() => getComputedStyle(document.getElementById("win-close")).getPropertyValue("--wails-draggable").trim()), "no-drag");
    await page.click("#win-min");
    await page.click("#win-max");
    await page.waitForFunction(() => document.getElementById("win-max").getAttribute("aria-label") === "Restore");
    await page.click("#win-close");
    const windowCalls = (await page.evaluate(() => window.__calls)).map((c) => c[0]).filter((c) => /^Window(Minimise|ToggleMaximise)$|^Quit$/.test(c));
    assert.deepEqual(windowCalls, ["WindowMinimise", "WindowToggleMaximise", "Quit"]);

    // Theme: auto, then light, then dark, each overriding the Windows setting, and remembered.
    const bg = () => page.evaluate(() => getComputedStyle(document.body).backgroundColor);
    assert.equal(await page.getAttribute("#theme-btn", "data-choice"), "auto");
    await page.click("#theme-btn");
    assert.equal(await page.getAttribute("html", "data-theme"), "light");
    assert.equal(await bg(), "rgb(245, 247, 249)");
    // The native window background follows the theme, so a light window never flashes dark.
    assert.deepEqual((await page.evaluate(() => window.__calls)).filter((c) => c[0] === "WindowSetBackgroundColour").at(-1), ["WindowSetBackgroundColour", 245, 247, 249, 255]);
    await page.click("#theme-btn");
    assert.equal(await page.getAttribute("html", "data-theme"), "dark");
    assert.equal(await bg(), "rgb(2, 12, 27)");
    if (shots) await page.screenshot({ path: join(shots, `ui-titlebar-dark-from-${scheme}.png`), clip: { x: 0, y: 0, width: 1320, height: 140 } });
    await page.reload();
    await page.waitForSelector("#titlebar:not([hidden])");
    assert.equal(await page.getAttribute("html", "data-theme"), "dark", "theme not remembered");
    await page.click("#theme-btn");
    assert.equal(await page.getAttribute("html", "data-theme"), null);
    assert.equal(await bg(), scheme === "dark" ? "rgb(2, 12, 27)" : "rgb(245, 247, 249)");

    // Site inventories: device annotations are applied, searchable, and saved
    // in the scan rather than in browser settings.
    await page.click("#scan-btn");
    await page.waitForFunction(() => document.getElementById("status-text").textContent.startsWith("Done:"));
    await page.fill("#site", "Clinic");
    await page.click('#rows tr[data-ip="192.168.50.20"]');
    await page.fill("#device-notes", '<img src=x onerror="window.__pwned=1">Owner Alice');
    await page.fill("#device-tags", "owner:Alice, location:MDF, cui:inside");
    await page.check("#device-favorite");
    await page.click("#apply-notes");
    await page.waitForFunction(() => window.__calls.some(c => c[0] === "SetAnnotation"));
    await page.check("#favorites-only");
    assert.equal(await page.locator("#rows tr").count(), 1);
    await page.uncheck("#favorites-only");
    await page.fill("#filter", "cui:inside");
    assert.equal(await page.locator("#rows tr").count(), 1);
    await page.fill("#filter", "");
    await page.click("#save-scan");
    await page.waitForFunction(() => window.__savedScan?.site === "Clinic");
    const savedScan = await page.evaluate(() => window.__savedScan);
    assert.equal(savedScan.hosts.find(h => h.ip === "192.168.50.20").favorite, true);
    assert.equal(savedScan.hosts.length, 6, "save must include unresponsive addresses even when hidden");
    assert.ok(!JSON.stringify(savedScan).includes("secret"), "scan persisted a credential");

    // Fixed actions use the selected address. Diagnostic output remains text.
    await page.click('#detail button[data-action="rdp"]');
    await page.click('#detail button[data-action="ping"]');
    await page.waitForFunction(() => document.getElementById("diagnostics-output").textContent.includes("Reply from"));
    assert.equal(await page.locator("#diagnostics img").count(), 0);
    assert.deepEqual((await page.evaluate(() => window.__calls)).filter(c => c[0] === "DeviceAction").at(-1), ["DeviceAction", "ping", "192.168.50.20", false]);
    await page.click("#diagnostics-close");
    await page.click('#detail button[data-action="wake"]');
    await page.click("#modal-cancel");
    assert.equal((await page.evaluate(() => window.__calls)).filter(c => c[0] === "WakeOnLAN").length, 0);
    await page.click('#detail button[data-action="wake"]');
    await page.click("#modal-ok");
    await page.waitForFunction(() => window.__calls.some(c => c[0] === "WakeOnLAN"));

    // Compare changes, address moves, missing hosts and unvisited addresses.
    await page.click("#compare-scan");
    await page.waitForSelector('#comparison:not([hidden])');
    assert.match(await page.textContent("#comparison-counts"), /1 new.*1 missing.*1 changed.*1 unchanged.*1 not assessed/);
    assert.match(await page.textContent("#comparison-list"), /Previous address: 192.168.50.19/);
    assert.equal(await page.locator("#comparison img").count(), 0);
    await page.selectOption("#change-filter", "missing");
    assert.equal(await page.locator("#comparison-list li").count(), 1);
    assert.match(await page.textContent("#comparison-list"), /<img src=x/);
    assert.equal(await page.evaluate(() => window.__pwned), undefined);
    if (shots) await page.screenshot({ path: join(shots, `ui-comparison-${scheme}.png`) });
    await page.click("#comparison-close");

    // A separate site clears annotations; opening the saved file restores them.
    await page.click("#settings-btn");
    await page.check("#snmp-on");
    await page.fill("#snmp-auth-pass", "previous-site-auth");
    await page.fill("#snmp-priv-pass", "previous-site-priv");
    await page.click("#settings-close");
    await page.click("#new-site");
    await page.waitForFunction(() => document.querySelectorAll("#rows tr").length === 0);
    assert.equal(await page.isChecked("#snmp-on"), false);
    assert.equal(await page.inputValue("#snmp-auth-pass"), "");
    assert.equal(await page.inputValue("#snmp-priv-pass"), "");
    await page.click("#load-scan");
    await page.waitForFunction(() => document.getElementById("site").value === "Clinic");
    await page.click('#rows tr[data-ip="192.168.50.20"]');
    assert.equal(await page.isChecked("#device-favorite"), true);
    assert.match(await page.inputValue("#device-notes"), /Owner Alice/);
    await page.fill("#site", "Unsaved rename");
    await page.click("#new-site");
    await page.waitForSelector("#modal:not([hidden])");
    await page.click("#modal-cancel");
    assert.equal(await page.inputValue("#site"), "Unsaved rename");

    await page.setViewportSize({ width: 960, height: 640 });
    const clippedControls = await page.$$eval(".inventory-bar button, .filters button, .filters select", nodes => nodes.filter(n => n.getBoundingClientRect().right > innerWidth).map(n => n.id));
    assert.deepEqual(clippedControls, [], "inventory controls outside the minimum window");

    assert.deepEqual(errors, [], "page errors");
    console.log(`ok  ${scheme} scheme`);
    await page.close();
  }
} catch (e) {
  failed = true;
  console.error(e);
} finally {
  await browser.close();
  server.close();
}
process.exit(failed ? 1 : 0);
