// Hostile seat names in the tally render as text (--lan, logged in).
// node tally_xss.js url pwfile shots
const { chromium } = require("playwright");
const fs = require("fs");
const [, , FD, PWF, SHOTS] = process.argv;
const PW = fs.readFileSync(PWF, "utf8").trim();
(async () => {
  const browser = await chromium.launch();
  const page = await (await browser.newContext()).newPage();
  const dialogs = [], errors = [];
  page.on("dialog", (d) => { dialogs.push(d.message()); d.dismiss(); });
  page.on("pageerror", (e) => errors.push(String(e)));
  await page.goto(FD + "/#tally");
  await page.fill("input[type=password]", PW);
  await page.keyboard.press("Enter");
  let r;
  for (let i = 0; i < 45; i++) {
    await page.waitForTimeout(2000);
    r = await page.evaluate(() => ({
      injected: document.querySelectorAll(
        "#tab-tally img, #tab-tally svg, #tab-tally script, #tab-tally b")
        .length,
      els: [...document.querySelectorAll("#tab-tally img, #tab-tally svg, #tab-tally script, #tab-tally b")].map((e) => e.outerHTML.slice(0, 70)),
      hostile: [...document.querySelectorAll("#tab-tally *")]
        .filter((e) => e.children.length === 0 &&
                /onerror|onload/.test(e.textContent))
        .map((e) => e.textContent).slice(0, 4),
    }));
    if (r.hostile.length >= 2) break;
  }
  console.log(JSON.stringify(r), "dialogs", JSON.stringify(dialogs),
              "errors", JSON.stringify(errors));
  await page.screenshot({ path: SHOTS + "/tally-hostile.png",
                          fullPage: true });
  await browser.close();
})().catch((e) => { console.error("FAILED", e); process.exit(1); });
