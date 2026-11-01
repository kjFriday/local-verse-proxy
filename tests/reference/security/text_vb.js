const { chromium } = require("playwright");
const fs = require("fs");
const [, , FD, PWF] = process.argv;
const PW = fs.readFileSync(PWF, "utf8").trim();
(async () => {
  const browser = await chromium.launch();
  const page = await (await browser.newContext()).newPage();
  const errors = [];
  page.on("pageerror", (e) => errors.push(String(e)));
  await page.goto(FD + "/#cards");
  await page.fill("input[type=password]", PW);
  await page.keyboard.press("Enter");
  await page.waitForSelector("#cards-list article");
  const r = await page.evaluate(() => [...document.querySelectorAll(
    "#cards-list article")].map((a) => [...a.querySelectorAll("h3,p")]
      .map((p) => p.textContent.replace(/\s+/g, " ")).join(" | ")
      .slice(0, 260)));
  for (const x of r) console.log(x);
  console.log("errors", JSON.stringify(errors));
  await browser.close();
})();
