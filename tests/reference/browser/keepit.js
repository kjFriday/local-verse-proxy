// Keyboard: Cancel card, then Keep it. Where does focus go?
const { chromium } = require('playwright');
const [url, seat] = process.argv.slice(2);
(async () => {
  const b = await chromium.launch();
  const p = await (await b.newContext()).newPage();
  await p.goto(url + '/#cards');
  await p.waitForFunction((s) => state && state.cards.some(
    (c) => c.seat === s), seat);
  const art = p.locator('#cards-list article', { has: p.locator('h3',
    { hasText: new RegExp('^' + seat + '$') }) });
  await art.locator('button.danger').focus();
  await p.keyboard.press('Enter');
  const a = await p.evaluate(() => document.activeElement.textContent);
  await p.keyboard.press('Enter');  // on "Keep it"
  const f = await p.evaluate(() => [document.activeElement.tagName,
    document.activeElement.textContent.slice(0, 20)]);
  await p.keyboard.press('Tab');
  const t = await p.evaluate(() => [document.activeElement.tagName,
    document.activeElement.textContent.slice(0, 20)]);
  console.log(JSON.stringify({ afterCancel: a, afterKeep: f,
                               nextTab: t }));
  await b.close();
})().catch((e) => { console.error('FAILED', e.message); process.exit(1); });
