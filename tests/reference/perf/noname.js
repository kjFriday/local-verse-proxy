// Cancel the card with no seat name, and the hostile-name cards, through
// the page. usage: node noname.js URL
const { chromium } = require('playwright');
(async () => {
  const [url] = process.argv.slice(2);
  const b = await chromium.launch();
  const p = await (await b.newContext({ viewport: { width: 390,
    height: 844 } })).newPage();
  const errs = [];
  p.on('pageerror', (e) => errs.push(String(e)));
  p.on('dialog', (d) => { errs.push('dialog ' + d.message()); d.dismiss(); });
  await p.goto(url + '/#cards');
  await p.waitForSelector('#pill.ok');
  await p.waitForSelector('#cards-list article');
  for (const name of ['(no seat name)', '"><script>alert(2)</script>',
                      "x' style='background:red", 'zz&lt;b&gt;bold']) {
    const card = p.locator('#cards-list article', { has:
      p.locator('h3', { hasText: name }) }).first();
    const n0 = await card.count();
    await card.scrollIntoViewIfNeeded();
    await card.locator('button.danger').click();
    const t = Date.now();
    await card.locator('button.solid').click();
    await p.waitForSelector('#cards-list .gone');
    const left = await p.$$eval('#cards-list article h3',
      (hs, n) => hs.filter((h) => h.textContent === n).length, name);
    console.log(`cancel ${JSON.stringify(name)}: found ${n0}, note in ` +
      `${Date.now() - t} ms, left after: ${left}, flash: ` +
      JSON.stringify(await p.textContent('#flash')));
    await p.waitForTimeout(6500);
  }
  console.log('errors/dialogs:', errs.length ? errs : 'none');
  await b.close();
})();
