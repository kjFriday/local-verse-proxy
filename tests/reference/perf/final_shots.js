// Final screenshots. Keys are masked before any shot.
// usage: node final_shots.js URL SHOTS
const { chromium } = require('playwright');
const [url, shots] = process.argv.slice(2);
const mask = () => { document.querySelector('#nk-key').value =
  'sk-' + '•'.repeat(22); };
(async () => {
  const b = await chromium.launch();
  for (const [w, hgt, scheme] of [[390, 844, 'light'], [390, 844, 'dark'],
      [1280, 800, 'light'], [1280, 800, 'dark']]) {
    const tag = `final-${w}x${hgt}-${scheme}`;
    const ctx = await b.newContext({ viewport: { width: w, height: hgt },
      colorScheme: scheme, deviceScaleFactor: w < 700 ? 2 : 1 });
    const p = await ctx.newPage();
    const errs = [];
    p.on('pageerror', (e) => errs.push(String(e)));
    p.on('console', (m) => { if (m.type() === 'error') errs.push(m.text()); });
    await p.goto(url + '/#cards');
    await p.waitForSelector('#pill.ok');
    await p.waitForSelector('#cards-list article');
    await p.waitForTimeout(300);
    await p.screenshot({ path: `${shots}/${tag}-cards.png` });
    // A card asking "are you sure?"
    const card = p.locator('#cards-list article').nth(1);
    await card.locator('button.danger').click();
    await p.waitForTimeout(150);
    await p.screenshot({ path: `${shots}/${tag}-confirm.png` });
    await card.locator('button.keep').click();
    await p.click('#tabs a[data-tab=new]');
    await p.waitForSelector('#models input');
    await p.screenshot({ path: `${shots}/${tag}-new.png` });
    await p.fill('#seat', 'bad name');
    await p.click('#mint-btn');
    await p.waitForTimeout(150);
    await p.screenshot({ path: `${shots}/${tag}-new-error.png` });
    await p.fill('#seat', `final-${w}-${scheme}`);
    await p.fill('#budget', '2.50');
    await p.click('#mint-btn');
    await p.waitForSelector('#newkey[open]');
    await p.evaluate(mask);
    await p.screenshot({ path: `${shots}/${tag}-newkey.png` });
    await p.click('#nk-done');
    await p.waitForSelector('#tab-cards:not([hidden])');
    await p.click('#tabs a[data-tab=tally]');
    await p.waitForSelector('#tally-models .bar-row');
    await p.screenshot({ path: `${shots}/${tag}-tally.png` });
    await p.click('.seg button[data-view=all]');
    await p.waitForTimeout(150);
    await p.screenshot({ path: `${shots}/${tag}-tally-all.png` });
    const sw = await p.evaluate(() =>
      document.documentElement.scrollWidth > innerWidth);
    // Clean up the card this run made.
    await p.click('#tabs a[data-tab=cards]');
    const mine = p.locator('#cards-list article', { has: p.locator('h3',
      { hasText: new RegExp(`^final-${w}-${scheme}$`) }) });
    await mine.locator('button.danger').click();
    await mine.locator('button.solid').click();
    await p.waitForSelector('#cards-list .gone');
    console.log(tag, 'hscroll', sw, 'errors', errs.length ? errs : 'none');
    await ctx.close();
  }
  await b.close();
})();
