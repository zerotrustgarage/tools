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
const types = { ".html": "text/html", ".css": "text/css", ".js": "text/javascript" };

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
