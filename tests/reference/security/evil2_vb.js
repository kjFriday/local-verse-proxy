// Cross-site page vs local (no password) Front Desk.
// node evil2_vb.js fdurl evilurl shots
const { chromium } = require("playwright");
const [, , FD, EVIL, SHOTS] = process.argv;
(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  await page.goto(FD + "/#cards");
  await page.waitForSelector("#cards-list article");
  const evil = await ctx.newPage();
  await evil.goto(EVIL + "/evil.html");
  await evil.waitForFunction(() => window.results && window.results.done,
                             null, { timeout: 15000 });
  console.log("evil results:", JSON.stringify(
    await evil.evaluate(() => window.results)));
  const fr = evil.frames().find((f) => f !== evil.mainFrame());
  let framed = "?";
  try { framed = (await fr.content()).length; } catch (e) { framed = "x"; }
  console.log("iframe content length:", framed);
  await evil.screenshot({ path: SHOTS + "/evil-frame.png" });
  const nav = evil.waitForNavigation({ timeout: 5000 }).catch(() => null);
  await evil.evaluate(() => document.getElementById("fm").submit());
  await nav;
  console.log("form post:", (await evil.textContent("body")).slice(0, 80));
  await page.waitForTimeout(1200);
  await page.reload();
  await page.waitForSelector("#cards-list article");
  const evilCards = await page.evaluate(() => [...document.querySelectorAll(
    "#cards-list h3")].map((h) => h.textContent)
    .filter((t) => t.startsWith("evil")));
  console.log("evil cards made:", JSON.stringify(evilCards));
  await browser.close();
})().catch((e) => { console.error("FAILED", e); process.exit(1); });
