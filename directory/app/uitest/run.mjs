// UI test for the ZTG Directory desktop frontend. It drives the real console
// logic: uitest/server serves the frontend with console.API over the demo
// directory behind a stand-in for the Wails binding. Never shipped.
//
//   node uitest/run.mjs [screenshot-dir]     (default uitest/out, gitignored)
//
// Requires the playwright package (createRequire honors NODE_PATH, so a global
// install works), a Chromium, and Go to run the server.
import { createRequire } from "node:module";
import { spawn } from "node:child_process";
import { mkdirSync } from "node:fs";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import assert from "node:assert/strict";

const { chromium } = createRequire(import.meta.url)("playwright");
const here = fileURLToPath(new URL(".", import.meta.url));
const app = join(here, "..");
const shots = resolve(process.argv[2] || join(here, "out"));
mkdirSync(shots, { recursive: true });

const servers = [];
async function startServer(...args) {
  const proc = spawn("go", ["run", "./uitest/server", "-frontend", "frontend", ...args], { cwd: app, stdio: ["pipe", "pipe", "inherit"] });
  servers.push(proc);
  return await new Promise((res, rej) => {
    let buf = "";
    const timer = setTimeout(() => rej(new Error("the UI test server did not start")), 180000);
    proc.stdout.on("data", (d) => {
      buf += d;
      const m = /listening (http:\/\/127\.0\.0\.1:\d+\/)/.exec(buf);
      if (m) { clearTimeout(timer); res(m[1]); }
    });
    proc.on("exit", (code) => rej(new Error("the UI test server exited with " + code)));
  });
}
const stopServers = () => { for (const p of servers) { try { p.stdin.end(); } catch { /* gone */ } } };

const browser = await chromium.launch(process.env.CHROMIUM ? { executablePath: process.env.CHROMIUM } : {});
let step = "start";
const log = (s) => { step = s; console.log("  " + s); };

async function newPage(origin, scheme = "dark", viewport = { width: 1440, height: 900 }) {
  const page = await browser.newPage({ viewport, colorScheme: scheme, deviceScaleFactor: 1 });
  page.problems = [];
  page.on("pageerror", (e) => page.problems.push("page error: " + e));
  page.on("console", (m) => { if (m.type() === "error" || m.type() === "warning") page.problems.push(`console ${m.type()}: ${m.text()}`); });
  page.on("request", (r) => {
    const u = r.url();
    if (!u.startsWith(origin) && !u.startsWith("data:")) page.problems.push("request to a non-local origin: " + u);
  });
  page.on("dialog", (d) => { page.problems.push("native dialog: " + d.message()); d.dismiss(); });
  return page;
}
// Screenshots are for judging layout: transient toasts are left out (one shot keeps them)
// and colour transitions after a theme change are allowed to finish.
async function shot(page, name, { toasts = false } = {}) {
  if (!toasts) await page.evaluate(() => document.querySelectorAll("#toasts .toast").forEach((t) => t.remove()));
  await page.waitForTimeout(250);
  await page.screenshot({ path: join(shots, name + ".png") });
}
const text = (page, sel) => page.locator(sel).first().innerText();

async function signIn(page, user = "ztg.tech") {
  await page.waitForSelector("#signin:not([hidden])");
  if (await page.isVisible("#notice-box")) {
    assert.equal(await page.isDisabled("#signin-btn"), true, "sign-in must wait for the notice to be acknowledged");
    await page.check("#notice-ack");
  }
  await page.fill("#user", user);
  await page.fill("#password", "Demo-Password-1");
  await page.click("#signin-btn");
  await page.waitForSelector("#shell:not([hidden])");
}

async function search(page, q, expectName) {
  await page.fill("#q", q);
  try {
    await page.waitForFunction(([n]) => [...document.querySelectorAll("#rows tr .name")].some((e) => e.textContent === n), [expectName], { timeout: 15000 });
  } catch (e) {
    // Report what the grid showed, so an intermittent failure explains itself.
    const seen = await page.evaluate(() => ({
      query: document.querySelector("#q")?.value,
      meta: document.querySelector("#results-meta")?.textContent,
      resultsHidden: document.querySelector("#page-results")?.hidden,
      names: [...document.querySelectorAll("#rows tr .name")].map((e) => e.textContent),
    }));
    throw new Error(`search for ${q} did not show ${expectName}: ${JSON.stringify(seen)}`);
  }
}

async function openFromSearch(page, q, name) {
  await search(page, q, name);
  await page.click(`#rows tr:has(.name:text-is("${name}"))`);
  await page.waitForFunction((n) => document.querySelector("#obj-name")?.textContent === n, name);
}

async function applyPlan(page, { ticket } = {}) {
  await page.waitForSelector("#plan-dlg[open] #plan-apply");
  if (ticket) {
    await page.fill("#plan-ticket", ticket);
    await page.waitForFunction(() => document.querySelector("#plan-ticket-state")?.textContent === "Recorded with every step" && !document.querySelector("#plan-apply").disabled);
  }
  assert.equal(await page.isDisabled("#plan-apply"), false, "Apply should be enabled");
  await page.click("#plan-apply");
  await page.waitForSelector("#plan-result");
}
const done = async (page) => { await page.click("#plan-done"); await page.waitForSelector("#plan-dlg", { state: "hidden" }); };

async function demoScenario() {
  const url = await startServer();
  const page = await newPage(url.replace(/\/$/, ""));

  log("sign-in: notice, wrong password, then the demo technician");
  await page.goto(url);
  await page.waitForSelector("#signin:not([hidden])");
  assert.match(await text(page, "#signin-hint"), /ztg\.tech/);
  assert.equal(await page.isVisible("#auth-demo"), true, "demo badge on the sign-in card");
  await shot(page, "01-sign-in");
  await page.check("#notice-ack");
  await page.fill("#user", "ztg.tech");
  await page.fill("#password", "wrong");
  await page.click("#signin-btn");
  await page.waitForSelector("#signin-err:not([hidden])");
  assert.match(await text(page, "#signin-err"), /incorrect/);
  await page.fill("#password", "Demo-Password-1");
  await page.click("#signin-btn");
  await page.waitForSelector("#shell:not([hidden])");
  assert.equal(await page.isVisible("#demo-badge"), true);
  assert.equal(await page.isVisible("#mfa-chip"), true, "password-only sessions show the MFA warning");
  assert.equal(await text(page, "#dc-name"), "dc01.lab.example");
  await page.waitForFunction(() => [...document.querySelectorAll("#home-tiles .tile-count")].every((e) => /^\d/.test(e.textContent)));
  assert.equal(await text(page, '#home-tiles [data-view="locked"] .tile-count'), "1");
  await shot(page, "02-overview");

  log("search with the keyboard and open Bea Okafor");
  await page.keyboard.press("/");
  assert.equal(await page.evaluate(() => document.activeElement.id), "q", "/ focuses the search box");
  await page.keyboard.type("okafor");
  await page.waitForFunction(() => document.querySelectorAll("#rows tr").length === 1 && document.querySelector("#rows tr .name").textContent === "Bea Okafor");
  assert.match(await text(page, "#results-meta"), /^1 result in \d+ ms$/);
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("Enter");
  await page.waitForFunction(() => document.querySelector("#obj-name")?.textContent === "Bea Okafor");
  assert.equal(await page.locator('#object .field[data-attr="title"] .synced').count(), 1, "title is marked as synchronized");

  log("edit the title inline, stage, review, apply");
  await page.click('#object .field[data-attr="title"] .f-value');
  await page.fill('#object .field[data-attr="title"] .inline-input', "Senior Accountant");
  await page.keyboard.press("Enter");
  await page.waitForSelector("#tray:not([hidden])");
  assert.equal(await text(page, "#tray-toggle"), "1 pending change");
  assert.match(await text(page, '#object .field[data-attr="title"]'), /Pending/);
  await shot(page, "03-object-pending-edit", { toasts: true });
  await page.click("#tray-review");
  await page.waitForSelector("#plan-changes");
  const changes = await text(page, "#plan-changes");
  assert.ok(changes.includes("Accountant") && changes.includes("Senior Accountant"), changes);
  await page.fill("#plan-ticket", "T-1001");
  await page.waitForFunction(() => document.querySelector("#plan-ticket-state")?.textContent === "Recorded with every step" && !document.querySelector("#plan-apply").disabled);
  await shot(page, "04-plan-review");
  await page.click("#plan-apply");
  await page.waitForSelector("#plan-result");
  await shot(page, "05-plan-applied");
  await done(page);
  await page.waitForFunction(() => document.querySelector("#obj-subtitle")?.textContent === "Senior Accountant");
  assert.equal(await page.isHidden("#tray"), true, "the applied edit leaves the tray");

  log("locked out view: unlock and reset Caleb's password");
  await page.click('#nav-views [data-view="locked"]');
  await page.waitForFunction(() => document.querySelector("#results-title").textContent === "Locked out" && !document.querySelector("#grid-wrap").classList.contains("loading") && document.querySelectorAll("#rows tr").length === 1);
  assert.match(await text(page, "#rows tr"), /Caleb Nakamura[\s\S]*Locked/);
  await page.click("#rows tr");
  await page.waitForFunction(() => document.querySelector("#obj-name")?.textContent === "Caleb Nakamura");
  assert.equal(await page.isVisible("#act-unlock"), true);
  await page.click("#act-reset");
  await page.waitForSelector("#ask-dlg[open]");
  assert.equal(await page.isChecked("#ask-must"), true, "must change at next logon is on by default");
  assert.equal(await page.isChecked("#ask-unlock"), true);
  await page.click("#ask-ok");
  await page.waitForSelector("#plan-changes");
  assert.equal(await page.locator("#plan-changes > li").count(), 2);
  await applyPlan(page);
  await page.waitForSelector("#plan-secrets");
  const masked = await text(page, "#plan-secrets .secret");
  assert.ok(/^•+$/.test(masked), "the password starts hidden");
  await page.click('#plan-secrets button:text-is("Reveal")');
  const pw = await text(page, "#plan-secrets .secret");
  assert.ok(pw.length >= 12 && !pw.includes("•"), "revealed password: " + pw.length);
  assert.match(await text(page, "#plan-secrets"), /Shown once and not recorded/);
  await shot(page, "06-one-time-password");
  await done(page);
  await page.waitForFunction(() => !document.querySelector("#obj-badges")?.textContent.includes("Locked out"));
  await page.waitForFunction(() => document.querySelectorAll("#rows tr").length === 0 && !document.querySelector("#grid-empty").hidden);

  log("new user from the Finance staff template with a live preview");
  await page.click("#new-btn");
  await page.click('#tpl-list [data-name="Finance staff"]');
  await page.waitForSelector("#tf-givenName");
  await page.fill("#tf-givenName", "Rowan");
  await page.fill("#tf-sn", "Ellis");
  await page.fill("#tf-title", "Analyst");
  await page.waitForFunction(() => document.querySelector("#preview-problems")?.textContent.includes("Employee ID"));
  assert.equal(await page.isDisabled("#tpl-create"), true, "create waits for required fields");
  await page.fill("#tf-employeeID", "E2001");
  await page.waitForFunction(() => document.querySelector('#preview-list dd[data-k="Account name"]')?.textContent === "rowan.ellis");
  assert.equal(await text(page, '#preview-list dd[data-k="Sign-in name (UPN)"]'), "rowan.ellis@contoso-labs.example");
  await page.waitForFunction(() => !document.querySelector("#tpl-create").disabled);
  await shot(page, "07-new-user-preview");
  await page.click("#tpl-create");
  await page.waitForSelector("#plan-changes");
  const create = await text(page, "#plan-changes");
  assert.ok(create.includes("Finance Team") && create.includes("rowan.ellis"), create);
  await applyPlan(page, { ticket: "T-1002" });
  await page.waitForSelector("#plan-secrets");
  await done(page);
  await page.waitForFunction(() => document.querySelector("#obj-name")?.textContent === "Rowan Ellis");

  log("a group named like markup stays text");
  const evil = '<img src=x onerror="window.__pwned=1">Ops';
  await page.click('#tpl-list [data-name="Security group"]');
  await page.fill("#tf-name", evil);
  await page.fill("#tf-description", "Operations access");
  await page.waitForFunction(() => !document.querySelector("#tpl-create").disabled);
  await page.click("#tpl-create");
  await applyPlan(page);
  await done(page);
  await page.waitForFunction((n) => document.querySelector("#obj-name")?.textContent === n, evil);
  assert.equal(await page.locator("#object img, #page-new img").count(), 0, "directory text never becomes markup");
  assert.equal(await page.evaluate(() => window.__pwned), undefined);

  log("add a member from the group page");
  await openFromSearch(page, "atlas", "Project Atlas");
  await page.click("#members-add");
  await page.waitForSelector("#pick-dlg[open]");
  await page.fill("#pick-q", "okafor");
  await page.waitForSelector('#pick-list .pick-row:has(.pick-name:text-is("Bea Okafor"))');
  await page.click('#pick-list .pick-row:has(.pick-name:text-is("Bea Okafor"))');
  await page.waitForSelector("#tray:not([hidden])");
  assert.match(await text(page, "#card-members"), /Bea Okafor[\s\S]*Pending add/);
  await page.click("#tray-review");
  await applyPlan(page);
  await shot(page, "08-group-member-applied");
  await done(page);
  await page.waitForFunction(() => /Bea Okafor/.test(document.querySelector("#card-members").textContent) && !/Pending/.test(document.querySelector("#card-members").textContent));
  await shot(page, "09-group-page");

  log("Tier 0: read-only page and a refused plan");
  await openFromSearch(page, "riley", "Admin Riley Chen");
  await page.waitForSelector("#tier0-note");
  assert.equal(await page.locator("#object .f-value.editable").count(), 0, "Tier 0 fields are read-only");
  assert.equal(await page.locator("#object .obj-actions").count(), 0, "no actions on a Tier 0 page");
  await shot(page, "10-tier0-page");
  await page.check('#rows tr:has(.name:text-is("Admin Riley Chen")) input[type=checkbox]');
  await page.waitForSelector("#bulk:not([hidden])");
  await page.click("#bulk-set");
  await page.waitForSelector("#ask-dlg[open]");
  await page.fill("#ask-value", "Operations");
  await page.click("#ask-ok");
  await page.waitForSelector("#plan-warnings li.crit");
  assert.match(await text(page, "#plan-warnings"), /cannot change title/);
  assert.equal(await page.isDisabled("#plan-apply"), true, "a blocked plan cannot be applied");
  assert.equal(await page.locator("#plan-tier0").count(), 1, "Tier 0 plans ask for the second confirmation");
  await shot(page, "11-tier0-refused");
  await page.click("#plan-dlg [data-close]");
  await page.click("#bulk-clear");

  log("browse the OU tree");
  await page.click("#obj-close");
  await page.click('#tree .tree-label[data-dn="OU=Contoso Labs,DC=lab,DC=example"]');
  await page.waitForSelector('#tree .tree-label[data-dn="OU=Staff,OU=Contoso Labs,DC=lab,DC=example"]');
  await page.waitForFunction(() => [...document.querySelectorAll("#rows tr .name")].some((e) => e.textContent === "Staff"));
  await page.click('#tree .tree-label[data-dn="OU=Staff,OU=Contoso Labs,DC=lab,DC=example"]');
  await page.click('#tree .tree-label[data-dn="OU=Finance,OU=Staff,OU=Contoso Labs,DC=lab,DC=example"]');
  await page.waitForFunction(() => {
    const names = [...document.querySelectorAll("#rows tr .name")].map((e) => e.textContent);
    return ["Avery Lindqvist", "Bea Okafor", "Caleb Nakamura", "Rowan Ellis"].every((n) => names.includes(n)) && document.querySelector("#results-title").textContent === "Finance";
  });
  assert.match(await text(page, "#results-crumbs"), /Contoso Labs[\s\S]*Staff/);
  await shot(page, "12-ou-browse");

  log("delete Rowan Ellis and restore from the Recycle Bin");
  await page.click('#rows tr:has(.name:text-is("Rowan Ellis"))');
  await page.waitForFunction(() => document.querySelector("#obj-name")?.textContent === "Rowan Ellis");
  await page.click("#act-delete");
  await applyPlan(page);
  await done(page);
  await page.waitForSelector("#object", { state: "hidden" });
  await page.click('#nav-main [data-page="bin"]');
  await page.fill("#bin-q", "Rowan");
  await page.waitForSelector('#bin-table tr:has-text("Rowan Ellis")');
  await shot(page, "13-recycle-bin");
  await page.click('#bin-table tr:has-text("Rowan Ellis") button');
  await applyPlan(page);
  await done(page);
  // The Recycle Bin refreshes after the dialog closes; wait for it so the
  // next search is not interleaved with that refresh.
  await page.waitForFunction(() => !document.querySelector("#bin-table")?.textContent.includes("Rowan Ellis"));
  await openFromSearch(page, "rowan", "Rowan Ellis");

  log("change records and the chain check");
  await page.click('#nav-main [data-page="records"]');
  await page.waitForSelector("#rec-table");
  const recs = await text(page, "#rec-table");
  for (const want of ["Set title on Bea Okafor", "Caleb Nakamura", "Rowan Ellis", "Project Atlas", "sign-in"]) assert.ok(recs.includes(want), "records lack " + want);
  assert.ok(!recs.includes(pw), "a temporary password reached the records");
  await page.click("#rec-table .rec-row");
  await page.waitForSelector("#rec-table .rec-detail:not([hidden])");
  await page.click("#verify-btn");
  await page.waitForSelector("#verify-ok");
  assert.match(await text(page, "#verify-ok"), /Chain intact/);
  await shot(page, "14-change-records");
  await page.selectOption("#rec-kind", "sign-in");
  await page.waitForFunction(() => {
    const kinds = [...document.querySelectorAll("#rec-table .rec-row .badge")].map((b) => b.textContent);
    return kinds.length > 0 && kinds.every((k) => k === "sign-in");
  });

  log("lock and unlock");
  await page.click("#lock-btn");
  await page.waitForSelector("#lock:not([hidden])");
  await page.fill("#lock-pw", "wrong");
  await page.click("#lock-btn-unlock");
  await page.waitForSelector("#lock-err:not([hidden])");
  await shot(page, "15-locked");
  await page.fill("#lock-pw", "Demo-Password-1");
  await page.click("#lock-btn-unlock");
  await page.waitForSelector("#lock", { state: "hidden" });

  log("light theme");
  await page.click('#nav-main [data-view="home"]');
  await openFromSearch(page, "okafor", "Bea Okafor");
  await page.click("#theme-btn");
  assert.equal(await page.evaluate(() => document.documentElement.dataset.theme), "light");
  await shot(page, "16-light-object");
  await page.click("#nav-main [data-page=new]");
  await page.click('#tpl-list [data-name="Engineering staff"]');
  await shot(page, "17-light-new-object");
  await page.click("#theme-btn");
  assert.equal(await page.evaluate(() => document.documentElement.dataset.theme), "dark");
  await page.click("#theme-btn");
  assert.equal(await page.evaluate(() => document.documentElement.dataset.theme), undefined, "back to the system theme");

  log("1100 x 700");
  await page.setViewportSize({ width: 1100, height: 700 });
  await page.click('#nav-views [data-view="inactive"]');
  await page.waitForSelector("#rows tr");
  await page.click("#rows tr");
  await page.waitForSelector("#obj-name");
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), "no horizontal scrolling at 1100 pixels");
  await shot(page, "18-small-results-object");
  await page.click("#obj-expand");
  await shot(page, "19-small-object-wide");
  await page.click("#obj-expand");

  log("toasts clear by themselves");
  await page.waitForFunction(() => document.querySelectorAll("#toasts .toast").length === 0, null, { timeout: 6000 });

  log("sign out");
  await page.click("#account-btn");
  await page.click("#updates-btn");
  await page.waitForFunction(() => /unreleased/.test(document.querySelector("#updates-out").textContent));
  await page.click("#signout-btn");
  await page.waitForSelector("#signin:not([hidden])");

  assert.deepEqual(page.problems, [], "page problems");
  await page.close();
}

async function setupScenario() {
  const url = await startServer("-setup");
  const page = await newPage(url.replace(/\/$/, ""), "light");
  log("first-run setup (light system theme)");
  await page.goto(url);
  await page.waitForSelector("#setup:not([hidden])");
  await shot(page, "20-setup");
  await page.fill("#s-domain", "lab.example");
  await page.fill("#s-radius-server", "duoproxy.lab.example:1812");
  await page.fill("#s-radius-secret", "not-a-real-secret");
  await page.click("#setup-btn");
  await page.waitForSelector("#setup-err:not([hidden])");
  assert.match(await text(page, "#setup-err"), /protected on Windows/);
  await page.click('#setup-form .seg label:has(input[value=""])');
  assert.equal(await page.isDisabled("#setup-btn"), true, "password-only needs the explicit tick");
  assert.match(await text(page, "#s-none"), /IA\.L2-3\.5\.3/);
  await page.check("#s-pwonly");
  await page.check("#s-ntlm");
  assert.equal(await page.isVisible("#s-ntlm-warn"), true);
  await page.uncheck("#s-ntlm");
  await page.fill("#s-notice", "Authorized use only. Activity is monitored and recorded.");
  await shot(page, "21-setup-password-only");
  await page.click("#setup-btn");
  await page.waitForSelector("#signin:not([hidden])");
  assert.equal(await page.isVisible("#auth-demo"), false);
  assert.match(await text(page, "#notice-text"), /Authorized use only/);
  await shot(page, "22-live-sign-in");
  await signIn(page, "LAB\\ztg.tech");
  assert.equal(await page.isVisible("#demo-badge"), false);
  assert.equal(await page.isVisible("#mfa-chip"), true);
  await openFromSearch(page, "okafor", "Bea Okafor");
  await shot(page, "23-live-light-object");
  assert.deepEqual(page.problems, [], "page problems");
  await page.close();
}

try {
  console.log("Demo scenario");
  await demoScenario();
  console.log("Setup scenario");
  await setupScenario();
  console.log("UI test passed. Screenshots: " + shots);
} catch (e) {
  console.error(`UI test failed at "${step}":`, e);
  process.exitCode = 1;
} finally {
  await browser.close();
  stopServers();
}
