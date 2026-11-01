// Card states (meters, icons, hints) light and dark, and the tally
// when every model is free. usage: node states_fix.js URL SHOTS
const { chromium } = require('playwright');
const [url, shots] = process.argv.slice(2);
const log = (...a) => console.log(...a);
(async () => {
  const b = await chromium.launch();
  for (const scheme of ['light', 'dark']) {
    const ctx = await b.newContext({ viewport: { width: 390, height: 844 },
      colorScheme: scheme, deviceScaleFactor: 2 });
    const p = await ctx.newPage();
    await p.goto(url + '/#cards');
    // Fresh cards: wait for the 10 s card check to see the updates.
    await p.waitForFunction(() => state && state.cards.some((c) =>
      c.seat === 'blocked-demo' && c.state === 'blocked') &&
      state.cards.some((c) => c.seat === 'amber-demo' && c.spent > 0.8),
      null, { timeout: 20000 });
    const info = await p.$$eval('#cards-list article', (as) =>
      as.slice(0, 3).map((a) => {
        const m = a.querySelector('.meter');
        const fill = getComputedStyle(m.firstChild).backgroundColor;
        const track = getComputedStyle(m).backgroundColor;
        const icon = getComputedStyle(a.querySelector('.badge .icon'))
          .color;
        const hint = [...a.querySelectorAll('p.small')].pop().textContent;
        return `${a.querySelector('h3').textContent}: fill ${fill} on ` +
          `track ${track}, icon ${icon}; "${hint.slice(0, 48)}…"`;
      }));
    log(`[${scheme}]`);
    info.forEach((x) => log('  ' + x));
    await p.evaluate(() => {
      document.querySelector('#connect').scrollIntoView();
      scrollBy(0, -140);
    });
    await p.screenshot({ path: `${shots}/390x844-${scheme}-states.png` });
    // Tally as it looks with free (Ollama-only) models.
    await p.click('#tabs a[data-tab=tally]');
    await p.waitForSelector('#tally-models .bar-row');
    await p.evaluate(() => {
      const s = JSON.parse(JSON.stringify(state));
      for (const r of [...s.tally.seats, ...s.tally.models]) {
        r.spend = 0;
        r.t_spend = 0;
      }
      render(s);
    });
    log('  free tally, by model:', JSON.stringify(await p.$$eval(
      '#tally-models .bar-row', (rs) => rs.map((r) => r.innerText
        .replace(/\n+/g, ' | ')))));
    log('  note:', await p.textContent('#tally-note'));
    log('  first bar width:', await p.$eval('#tally-seats .fill',
      (f) => f.style.width));
    await p.screenshot({ path: `${shots}/390x844-${scheme}-tally-free.png`
    });
    await ctx.close();
  }
  await b.close();
})();
