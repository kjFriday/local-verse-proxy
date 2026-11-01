// Hostile seat names and new refill fields render as text; game seat
// confirm; cancel through the page. node xss_vb.js url pwfile shots
const { chromium } = require("playwright");
const fs = require("fs");
const [, , FD, PWF, SHOTS] = process.argv;
const PW = fs.readFileSync(PWF, "utf8").trim();
(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 390,
                                                      height: 844 } });
  const page = await ctx.newPage();
  const dialogs = [], errors = [];
  page.on("dialog", (d) => { dialogs.push(d.message()); d.dismiss(); });
  page.on("pageerror", (e) => errors.push(String(e)));
  await page.goto(FD + "/#cards");
  await page.fill("input[type=password]", PW);
  await page.keyboard.press("Enter");
  await page.waitForSelector("#cards-list article");
  await page.waitForTimeout(12000); // a key refresh lands
  const r = await page.evaluate(() => {
    const list = document.querySelector("#cards-list");
    const arts = [...list.querySelectorAll("article")];
    const pick = (s) => arts.filter((a) =>
      a.querySelector("h3").textContent.includes(s))
      .map((a) => a.innerText.replace(/\s+/g, " ").slice(0, 400));
    return {
      cards: arts.length,
      injected: list.querySelectorAll("img, svg, script, iframe, b")
        .length,
      img: pick("<img src=x"), svg: pick("game-<svg"),
      script: pick("<script>"), dbx: pick("game-dbx"),
      rtl: pick("gnp.exe"),
      r1w: pick("vb-r-1w"),
      cookie: document.cookie,
    };
  });
  console.log(JSON.stringify(r, null, 1));
  await page.screenshot({ path: SHOTS + "/xss-cards.png",
                          fullPage: true });
  // game seat confirm step
  const g = page.locator("article", { hasText: "game-<svg" });
  await g.getByRole("button", { name: "Cancel card" }).click();
  const conf = await g.locator(".confirm").innerText();
  console.log("game confirm:", conf.replace(/\s+/g, " "));
  await g.screenshot({ path: SHOTS + "/game-confirm.png" });
  await g.getByRole("button", { name: "Keep it" }).click();
  // cancel the <img> card through the page
  const a = page.locator("article", { hasText: "<img src=x" });
  await a.getByRole("button", { name: "Cancel card" }).click();
  await a.getByRole("button", { name: "Yes, cancel it" }).click();
  await page.waitForTimeout(1500);
  const left = await page.evaluate(() => ({
    img: [...document.querySelectorAll("#cards-list h3")]
      .some((h) => h.textContent.includes("<img")),
    note: (document.querySelector(".card.gone") || {}).innerText,
  }));
  console.log("after cancel:", JSON.stringify(left));
  console.log("dialogs:", JSON.stringify(dialogs),
              "page errors:", JSON.stringify(errors));
  await browser.close();
})().catch((e) => { console.error("FAILED", e); process.exit(1); });
