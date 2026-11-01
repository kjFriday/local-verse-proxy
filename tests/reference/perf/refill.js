const { chromium } = require('playwright');
(async () => {
  const [url, shot] = process.argv.slice(2);
  const b = await chromium.launch();
  const p = await (await b.newContext({ viewport: { width: 390, height: 844 },
    deviceScaleFactor: 2 })).newPage();
  await p.goto(url + '/#cards');
  await p.waitForSelector('#cards-list article');
  const card = p.locator('#cards-list article', { has: p.locator('h3',
    { hasText: /^refill-demo$/ }) });
  await card.scrollIntoViewIfNeeded();
  console.log(JSON.stringify(await card.innerText()));
  await card.screenshot({ path: shot });
  await b.close();
})();
