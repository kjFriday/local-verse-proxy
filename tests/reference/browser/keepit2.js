// Keep it: focus back on the same card's Cancel card, by Enter,
// Space, a click, and after a live redraw while the box is open.
// node keepit2.js URL SEAT
const { chromium } = require('playwright');
const [url, seat] = process.argv.slice(2);
(async () => {
  const b = await chromium.launch();
  const p = await (await b.newContext({ viewport: { width: 390,
    height: 844 } })).newPage();
  const errs = [];
  p.on('pageerror', (e) => errs.push(e.message));
  await p.goto(url + '/#cards');
  await p.waitForFunction((s) => state && state.cards.some(
    (c) => c.seat === s), seat);
  const art = () => p.locator('#cards-list article', { has: p.locator(
    'h3', { hasText: new RegExp('^' + seat + '$') }) });
  const where = () => p.evaluate(() => {
    const f = document.activeElement;
    const c = f.closest && f.closest('article');
    return [f.tagName, f.textContent.slice(0, 20),
            c ? c.querySelector('h3').textContent : null,
            Math.round(f.getBoundingClientRect().top)];
  });
  for (const how of ['Enter', 'Space', 'click', 'redraw']) {
    await art().locator('button.danger').focus();
    await p.keyboard.press('Enter');
    const a = await where();
    if (how === 'redraw') {
      // A new state comes in while the box is open: card drawn again.
      await p.evaluate(() => { const s = JSON.parse(JSON.stringify(state));
        for (const c of s.cards) c.spent += 1e-9; render(s); });
      await p.waitForTimeout(100);
    }
    const mid = await where();
    if (how === 'click') await art().locator('button.keep').click();
    else if (how === 'Space') await p.keyboard.press(' ');
    else await p.keyboard.press('Enter');
    await p.waitForTimeout(100);
    console.log(how, JSON.stringify({ after: a, mid, keep: await where(),
      boxes: await p.locator('.confirm').count() }));
  }
  console.log('errs', JSON.stringify(errs));
  await b.close();
})().catch((e) => { console.error('FAILED', e.message); process.exit(1); });
