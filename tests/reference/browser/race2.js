// The card being cancelled is redrawn (live spend update) or leaves
// the list while the cancel request is in flight. Where does the note
// land, and does a failure show? node race2.js URL SEAT SHOTDIR
const { chromium } = require('playwright');
const [url, seat, dir] = process.argv.slice(2);
(async () => {
  const b = await chromium.launch();
  const p = await (await b.newContext({ viewport: { width: 390,
    height: 844 }, deviceScaleFactor: 2 })).newPage();
  await p.goto(url + '/#cards');
  await p.waitForFunction((s) => state && state.cards.some(
    (c) => c.seat === s), seat, { timeout: 20000 });
  const find = (s) => {
    const a = [...document.querySelectorAll('#cards-list article')]
      .find((x) => x.querySelector('h3').textContent === s);
    return a || null;
  };
  for (const mode of ['fail', 'redrawn-ok', 'gone-ok']) {
    let release;
    const gate = new Promise((ok) => { release = ok; });
    await p.route('**/api/revoke', async (route) => {
      await gate;
      // Fake answers: the card itself stays, so each case can rerun.
      if (mode === 'fail') await route.fulfill({ status: 400,
        contentType: 'application/json', body: JSON.stringify({ error:
        'The proxy is not answering. Is it running?' }) });
      else await route.fulfill({ status: 200, contentType:
        'application/json', body: JSON.stringify({ ok: true, seat }) });
    });
    const card = p.locator('#cards-list article', { has: p.locator('h3',
      { hasText: new RegExp('^' + seat + '$') }) });
    await card.scrollIntoViewIfNeeded();
    const before = await p.evaluate((s) => {
      const list = [...document.querySelector('#cards-list').children];
      const a = [...document.querySelectorAll('#cards-list article')]
        .find((x) => x.querySelector('h3').textContent === s);
      return { index: list.indexOf(a),
        top: Math.round(a.getBoundingClientRect().top),
        scrollY: Math.round(scrollY) };
    }, seat);
    await card.locator('button.danger').click();
    await card.locator('button.solid').click();
    // A live update lands while the request waits.
    const during = await p.evaluate(([s, mode]) => {
      const st = JSON.parse(JSON.stringify(state));
      if (mode === 'gone-ok') {
        st.cards = st.cards.filter((c) => c.seat !== s);
      } else st.cards.find((c) => c.seat === s).spent += 0.01;
      const old = drawn.get(state.cards.find((c) => c.seat === s).id);
      render(st);
      const a = [...document.querySelectorAll('#cards-list article')]
        .find((x) => x.querySelector('h3').textContent === s);
      const yes = a && a.querySelector('.solid');
      return { redrawn: !!a && a !== (old && old[1]),
        on_list: !!a, yes: yes && yes.textContent,
        yes_disabled: yes && yes.disabled };
    }, [seat, mode]);
    release();
    await p.waitForTimeout(600);
    if (mode === 'fail') {
      const r = await p.evaluate((s) => {
        const a = [...document.querySelectorAll('#cards-list article')]
          .find((x) => x.querySelector('h3').textContent === s);
        const yes = a && a.querySelector('.solid');
        return { error_visible: !!a && /not answering/.test(a.innerText),
          yes: yes && yes.textContent, yes_disabled: yes && yes.disabled,
          flash: document.querySelector('#flash').textContent };
      }, seat);
      console.log(mode, 'during:', JSON.stringify(during), 'after:',
                  JSON.stringify(r));
      await p.screenshot({ path: dir + '/race-fail.png' });
      await card.locator('button.keep').click();
    } else {
      const r = await p.evaluate(() => {
        const g = document.querySelector('#cards-list .gone');
        const list = [...document.querySelector('#cards-list').children];
        return { note_index: list.indexOf(g), note_top: Math.round(
          g.getBoundingClientRect().top), note_height: g.offsetHeight,
          scrollY: Math.round(scrollY) };
      });
      console.log(mode, 'before:', JSON.stringify(before), 'during:',
                  JSON.stringify(during), 'note:', JSON.stringify(r));
      await p.screenshot({ path: dir + '/race-' + mode + '.png' });
      // Let the note go and the real state come back.
      await p.waitForTimeout(6500);
      await p.evaluate(() => load());
      await p.waitForTimeout(500);
    }
    await p.unroute('**/api/revoke');
  }
  await b.close();
})().catch((e) => { console.error('FAILED', e.message); process.exit(1); });
