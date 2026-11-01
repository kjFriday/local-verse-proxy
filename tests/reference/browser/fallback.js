// Tally Today vs All time as the page shows them, seats and models.
// node fallback.js URL OUTPREFIX
const { chromium } = require('playwright');
const [url, out] = process.argv.slice(2);
(async () => {
  const b = await chromium.launch();
  const p = await (await b.newContext({ viewport: { width: 390,
    height: 844 }, deviceScaleFactor: 2 })).newPage();
  await p.goto(url + '/#tally');
  await p.waitForSelector('#pill.ok');
  await p.waitForFunction(() => state && state.tally);
  await p.waitForTimeout(800);
  for (const v of ['today', 'all']) {
    await p.click(`.seg button[data-view=${v}]`);
    await p.waitForTimeout(300);
    const t = await p.evaluate(() => {
      const rows = (sel) => [...document.querySelectorAll(sel + ' > *')]
        .map((r) => r.textContent.replace(/\s+/g, ' ').trim()).slice(0, 4);
      return { source: state.tally.source, why: state.tally.why,
        tiles: document.querySelector('#tally-tiles').textContent,
        seats: rows('#tally-seats'), models: rows('#tally-models'),
        note: document.querySelector('#tally-note').textContent };
    });
    console.log(v, JSON.stringify(t, null, 1));
    await p.screenshot({ path: `${out}-${v}.png`, fullPage: true });
  }
  await b.close();
})().catch((e) => { console.error('FAILED', e.message); process.exit(1); });
