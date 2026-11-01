// XSS, clickjacking, CSRF and DNS-rebinding checks in Chromium.
// node xss.js <frontdesk-url> <evil-url> <rebind-url> <shots-dir>
const { chromium } = require("playwright");
const [, , FD, EVIL, REBIND, SHOTS] = process.argv;

(async () => {
  const browser = await chromium.launch({
    args: ["--host-resolver-rules=MAP rebind.test 127.0.0.1"],
  });
  const ctx = await browser.newContext({ viewport: { width: 390,
                                                      height: 844 } });
  const page = await ctx.newPage();
  const dialogs = [], errors = [], consoleMsgs = [];
  page.on("dialog", (d) => { dialogs.push(d.message()); d.dismiss(); });
  page.on("pageerror", (e) => errors.push(String(e)));
  page.on("console", (m) => consoleMsgs.push(m.type() + ": " + m.text()));

  // 1. Hostile seat names render as text.
  await page.goto(FD + "/#cards");
  await page.waitForSelector("#cards-list article");
  await page.waitForTimeout(1500);
  const r = await page.evaluate(() => {
    const list = document.querySelector("#cards-list");
    return {
      cards: list.querySelectorAll("article").length,
      injectedEls: list.querySelectorAll("img, svg, script, iframe, b, i")
        .length,
      styledAttr: [...list.querySelectorAll("[style]")]
        .filter((e) => /background/.test(e.getAttribute("style"))).length,
      titles: [...list.querySelectorAll("h3")].map((h) => h.textContent),
      cookie: document.cookie,
      bodyHasSk: /sk-[A-Za-z0-9_-]{20,}/.test(document.body.innerHTML),
    };
  });
  console.log("cards view:", JSON.stringify(r));
  await page.screenshot({ path: SHOTS + "/xss-cards.png", fullPage: true });
  await page.goto(FD + "/#tally");
  await page.waitForTimeout(800);
  const t = await page.evaluate(() => ({
    injectedEls: document.querySelectorAll(
      "#tab-tally img, #tab-tally svg, #tab-tally script").length,
    labels: [...document.querySelectorAll("#tab-tally .top-line span")]
      .map((s) => s.textContent).filter((x) => !x.startsWith("$")),
  }));
  console.log("tally view:", JSON.stringify(t));
  await page.screenshot({ path: SHOTS + "/xss-tally.png", fullPage: true });

  // 2. Cancel the <img> card through the page (alias passed as argv).
  await page.goto(FD + "/#cards");
  await page.waitForSelector("#cards-list article");
  const art = page.locator("article", { hasText: "<img src=x" });
  await art.getByRole("button", { name: "Cancel card" }).click();
  await art.getByRole("button", { name: "Yes, cancel it" }).click();
  await page.waitForTimeout(1500);
  const left = await page.evaluate(() => [...document.querySelectorAll(
    "#cards-list h3")].map((h) => h.textContent));
  console.log("after cancel <img> card:", JSON.stringify(left));

  // 3. A malicious page on another origin, admin's browser.
  const evil = await ctx.newPage();
  evil.on("dialog", (d) => { dialogs.push("evil:" + d.message());
                             d.dismiss(); });
  const evilConsole = [];
  evil.on("console", (m) => evilConsole.push(m.text()));
  await evil.goto(EVIL + "/evil.html");
  await evil.waitForFunction(() => window.results && window.results.done,
                             null, { timeout: 15000 });
  const res = await evil.evaluate(() => window.results);
  console.log("evil page results:", JSON.stringify(res));
  const frame = evil.frames().find((f) => f !== evil.mainFrame());
  console.log("iframe url:", frame ? frame.url() : "(none)",
              "| framed blocked msgs:",
              evilConsole.filter((m) => /frame|ancestor|X-Frame/i.test(m))
                .length);
  await evil.screenshot({ path: SHOTS + "/evil-frame.png" });
  // auto-submitting text/plain form
  const nav = evil.waitForNavigation({ timeout: 5000 }).catch(() => null);
  await evil.evaluate(() => document.getElementById("fm").submit());
  await nav;
  console.log("form post landed on:", evil.url(), "body:",
              (await evil.textContent("body")).slice(0, 80));

  // 4. DNS rebinding: rebind.test -> 127.0.0.1, same port.
  const rb = await ctx.newPage();
  const resp = await rb.goto(REBIND + "/");
  console.log("rebind GET / status:", resp.status(), "body:",
              (await rb.textContent("body")).slice(0, 80));
  const rbApi = await rb.evaluate(async () => {
    const out = {};
    for (const p of ["/api/session", "/api/state"]) {
      const x = await fetch(p);
      out[p] = x.status + " " + (await x.text()).slice(0, 50);
    }
    return out;
  });
  console.log("rebind same-origin API:", JSON.stringify(rbApi));

  // Final: what cards exist now (evil ones must not).
  await page.goto(FD + "/#cards");
  await page.waitForSelector("#cards-list article");
  await page.waitForTimeout(800);
  const final = await page.evaluate(() => [...document.querySelectorAll(
    "#cards-list h3")].map((h) => h.textContent));
  console.log("final cards:", JSON.stringify(final));
  console.log("dialogs:", JSON.stringify(dialogs));
  console.log("page errors:", JSON.stringify(errors));
  console.log("console:", JSON.stringify(consoleMsgs.slice(0, 10)));
  await browser.close();
})().catch((e) => { console.error("FAILED", e); process.exit(1); });
