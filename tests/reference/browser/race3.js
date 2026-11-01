// Cancel race, on real game cards. node race3.js URL SEAT OTHER DIR
// 1 fail: cancel answer fails while the card is redrawn.
// 2 two: while SEAT's cancel waits (then fails), the user opens
//   OTHER's confirm box. Is SEAT's error visible anywhere?
// 3 two-ok: same, but SEAT's cancel succeeds (faked): does OTHER's
//   open confirm box survive?
// 4 real: a REAL cancel of SEAT while its card is redrawn.
const { chromium } = require('playwright');
const [url, seat, other, dir] = process.argv.slice(2);
const art = (s) => [...document.querySelectorAll('#cards-list article')]
  .find((x) => x.querySelector('h3').textContent === s) || null;
(async () => {
  const b = await chromium.launch();
  const p = await (await b.newContext({ viewport: { width: 390,
    height: 844 }, deviceScaleFactor: 2 })).newPage();
  const errs = [];
  p.on('pageerror', (e) => errs.push(e.message));
  await p.goto(url + '/#cards');
  await p.waitForFunction((s) => state && state.cards.some(
    (c) => c.seat === s), seat, { timeout: 20000 });
  await p.evaluate('window.art = ' + art.toString());
  const card = (s) => p.locator('#cards-list article', { has: p.locator(
    'h3', { hasText: new RegExp('^' + s + '$') }) });
  for (const mode of ['fail', 'two', 'two-ok', 'real']) {
    let release;
    const gate = new Promise((ok) => { release = ok; });
    await p.route('**/api/revoke', async (route) => {
      await gate;
      if (mode === 'fail' || mode === 'two') await route.fulfill({
        status: 502, contentType: 'application/json', body:
        JSON.stringify({ error: 'The proxy is not answering.' }) });
      else if (mode === 'two-ok') await route.fulfill({ status: 200,
        contentType: 'application/json', body: '{"ok":true}' });
      else await route.continue();
    });
    await card(seat).scrollIntoViewIfNeeded();
    const before = await p.evaluate((s) => ({
      index: [...document.querySelector('#cards-list').children]
        .indexOf(art(s)), scrollY: Math.round(scrollY) }), seat);
    await card(seat).locator('button.danger').click();
    await card(seat).locator('button.solid').click();
    if (mode === 'two' || mode === 'two-ok') {
      await card(other).locator('button.danger').click();
    } else {
      await p.evaluate((s) => {
        const st = JSON.parse(JSON.stringify(state));
        st.cards.find((c) => c.seat === s).spent += 0.001;
        render(st);
      }, seat);
    }
    release();
    await p.waitForTimeout(800);
    const r = await p.evaluate(([s, o]) => {
      const a = art(s), b = art(o);
      const g = document.querySelector('#cards-list .gone');
      const list = [...document.querySelector('#cards-list').children];
      return {
        seat_on_list: !!a,
        seat_error_visible: !!a && /not answering/.test(a.innerText),
        seat_yes: a && a.querySelector('.solid') &&
          a.querySelector('.solid').textContent,
        other_confirm_open: !!b && !!b.querySelector('.confirm'),
        note_index: g ? list.indexOf(g) : null,
        scrollY: Math.round(scrollY),
        flash: document.querySelector('#flash').textContent };
    }, [seat, other]);
    console.log(mode, 'before', JSON.stringify(before), 'after',
                JSON.stringify(r));
    await p.screenshot({ path: `${dir}/race3-${mode}.png` });
    await p.unroute('**/api/revoke');
    // Back to a clean page for the next case.
    await p.evaluate(() => { confirming = null; pending.clear();
      cancelled.clear(); });
    await p.waitForTimeout(6500);
    await p.evaluate(() => load());
    await p.waitForTimeout(500);
    await p.evaluate(() => renderCards());
  }
  console.log('errs', JSON.stringify(errs));
  await b.close();
})().catch((e) => { console.error('FAILED', e.message); process.exit(1); });
