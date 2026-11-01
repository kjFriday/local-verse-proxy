// The card being cancelled changes (live spend update) while the
// cancel request is in flight. Where does the note land? And if the
// cancel fails, is the error visible?
const { chromium } = require('playwright');
const [url, shot] = process.argv.slice(2);
(async () => {
  const b = await chromium.launch();
  const p = await (await b.newContext({ viewport: { width: 390,
    height: 844 }, deviceScaleFactor: 2 })).newPage();
  await p.goto(url + '/#cards');
  await p.waitForFunction(() => state && state.cards.some(
    (c) => c.seat === 'seat150b'), null, { timeout: 20000 });
  for (const fail of [true, false]) {
    let release;
    const gate = new Promise((ok) => { release = ok; });
    await p.route('**/api/revoke', async (route) => {
      await gate;
      if (fail) await route.fulfill({ status: 400, contentType:
        'application/json', body: JSON.stringify({ error:
        'The proxy is not answering. Is it running?' }) });
      else await route.continue();
    });
    const card = p.locator('#cards-list article', { has: p.locator('h3',
      { hasText: /^seat150b$/ }) });
    await card.scrollIntoViewIfNeeded();
    const y0 = (await card.boundingBox()).y;
    await card.locator('button.danger').click();
    await card.locator('button.solid').click();
    // A live update lands: this card's spend changed.
    await p.evaluate(() => {
      const s = JSON.parse(JSON.stringify(state));
      s.cards.find((c) => c.seat === 'seat150b').spent += 0.01;
      render(s);
    });
    release();
    await p.waitForTimeout(600);
    if (fail) {
      const r = await p.evaluate(() => {
        const a = [...document.querySelectorAll('#cards-list article')]
          .find((x) => x.querySelector('h3').textContent === 'seat150b');
        return { error_visible: !!a && /not answering/.test(a.innerText),
          yes_button: a && a.querySelector('.solid') &&
            a.querySelector('.solid').textContent };
      });
      console.log('cancel FAILED during a live update:', JSON.stringify(r));
      await p.screenshot({ path: shot.replace('.png', '-fail.png') });
      await card.locator('button.keep').click();
    } else {
      const r = await p.evaluate(() => {
        const g = document.querySelector('#cards-list .gone');
        const list = [...document.querySelector('#cards-list').children];
        return { note_index: list.indexOf(g), note_top: Math.round(
          g.getBoundingClientRect().top), note_height: g.offsetHeight,
          scrollY: Math.round(scrollY) };
      });
      console.log('cancel OK during a live update: card was at y', 
        Math.round(y0), JSON.stringify(r));
      await p.screenshot({ path: shot });
    }
    await p.unroute('**/api/revoke');
  }
  await b.close();
})();
