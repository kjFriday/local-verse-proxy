// Hostile seat names stay text across the per-minute card redraw and
// the tally. node xss3_vb.js url pwfile shots
const { chromium } = require("playwright");
const fs = require("fs");
const [, , FD, PWF, SHOTS] = process.argv;
const PW = fs.readFileSync(PWF, "utf8").trim();
const SEL = "img, svg, script, iframe, b, object, embed";
(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 390,
                                                      height: 844 },
                                         locale: "de-DE" });
  const page = await ctx.newPage();
  const dialogs = [], errors = [];
  page.on("dialog", (d) => { dialogs.push(d.message()); d.dismiss(); });
  page.on("pageerror", (e) => errors.push(String(e)));
  page.on("console", (m) => { if (m.type() === "error" || /Content Security/.test(m.text())) errors.push("console: " + m.text().slice(0, 120)); });
  await page.goto(FD + "/#cards");
  await page.fill("input[type=password]", PW);
  await page.keyboard.press("Enter");
  await page.waitForSelector("#cards-list article");
  await page.waitForTimeout(12000);
  const check = () => page.evaluate((SEL) => {
    const list = document.querySelector("#cards-list");
    const arts = [...list.querySelectorAll("article")];
    return {
      cards: arts.length,
      injected: [...list.querySelectorAll(SEL)]
        .filter((e) => !e.closest("button, .icon"))
        .map((e) => e.outerHTML.slice(0, 60)),
      hostile: arts.map((a) => a.querySelector("h3").textContent)
        .filter((t) => /[<>"]|‮/.test(t)),
      cookie: document.cookie,
      allSvg: list.querySelectorAll("svg").length,
    };
  }, SEL);
  const a = await check();
  console.log("first:", JSON.stringify(a));
  // wait past a minute change for tickCards
  const m0 = new Date().getMinutes();
  while (new Date().getMinutes() === m0) await page.waitForTimeout(1000);
  await page.waitForTimeout(3000);
  const b = await check();
  console.log("after minute redraw:", JSON.stringify(b));
  await page.screenshot({ path: SHOTS + "/xss3-cards.png",
                          fullPage: true });
  await page.goto(FD + "/#tally");
  await page.waitForTimeout(6000);
  const t = await page.evaluate((SEL) => ({
    injected: [...document.querySelectorAll("#tab-tally *")]
      .filter((e) => e.matches(SEL)).map((e) => e.outerHTML.slice(0, 60)),
    hostile: [...document.querySelectorAll("#tab-tally *")]
      .filter((e) => e.children.length === 0 &&
              /onerror|onload/.test(e.textContent))
      .map((e) => e.textContent).slice(0, 4),
  }), SEL);
  console.log("tally:", JSON.stringify(t));
  console.log("dialogs:", JSON.stringify(dialogs),
              "page errors:", JSON.stringify(errors));
  await browser.close();
})().catch((e) => { console.error("FAILED", e); process.exit(1); });
