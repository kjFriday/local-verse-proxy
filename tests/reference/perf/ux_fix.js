// UX checks for the fixes, plus phone screenshots. Keys are compared
// inside the page and never printed. usage: node ux_fix.js URL SHOTS
const { chromium } = require('playwright');
const fs = require('fs');
const [base, shots] = process.argv.slice(2);
fs.mkdirSync(shots, { recursive: true });
const log = (...a) => console.log(...a);

async function layout(p, label) {
  const r = await p.evaluate(() => {
    const small = [];
    for (const el of document.querySelectorAll(
        'button, a, input, select, textarea, .check')) {
      const b = el.getBoundingClientRect();
      if (!b.width || !b.height || el.closest('[hidden]')) continue;
      if (el.matches('.check input, .offscreen')) continue;
      if (b.height < 44) small.push(el.id || el.className);
    }
    return { sw: document.documentElement.scrollWidth,
             iw: window.innerWidth, small };
  });
  log(`  ${label}: hscroll ${r.sw > r.iw ? 'YES' : 'no'}, ` +
      `targets<44px ${r.small.length ? r.small.join(',') : 'none'}`);
}

async function rect(p, sel) {
  return p.evaluate((s) => {
    const e = document.querySelector(s);
    if (!e) return null;
    const b = e.getBoundingClientRect();
    return { top: Math.round(b.top), bottom: Math.round(b.bottom) };
  }, sel);
}

(async () => {
  const b = await chromium.launch();
  let n = +(process.env.START || 0);
  for (const [w, hgt] of [[390, 844], [360, 740]]) {
    for (const scheme of ['light', 'dark']) {
      const tag = `${w}x${hgt}-${scheme}`;
      log(`[${tag}]`);
      const ctx = await b.newContext({ viewport: { width: w, height: hgt },
        colorScheme: scheme, deviceScaleFactor: 2 });
      await ctx.grantPermissions(['clipboard-read', 'clipboard-write'],
                                 { origin: base });
      const p = await ctx.newPage();
      const errs = [];
      p.on('console', (m) => { if (m.type() === 'error') errs.push(m.text()); });
      p.on('pageerror', (e) => errs.push(String(e)));
      await p.goto(base + '/#cards');
      await p.waitForSelector('#cards-list article');
      await p.waitForSelector('#pill.ok');
      await layout(p, 'cards');
      log('  base URL on Key cards:', await p.isVisible('#connect'),
          await p.inputValue('#seat-url'));
      await p.screenshot({ path: `${shots}/${tag}-cards.png` });
      const hdr = (await rect(p, 'header.top')).bottom;

      // Form errors sit next to their field, in view.
      await p.click('#tabs a[data-tab=new]');
      await p.waitForSelector('#models input');
      await layout(p, 'new');
      await p.screenshot({ path: `${shots}/${tag}-new.png` });
      for (const [what, seat, budget] of [['bad seat', 'bad seat', '5'],
          ['bad budget', 'okname', 'five']]) {
        await p.evaluate(() => window.scrollTo(0, 99999));
        await p.fill('#seat', seat);
        await p.fill('#budget', budget);
        await p.evaluate(() => window.scrollTo(0, 99999));
        await p.click('#mint-btn');
        await p.waitForTimeout(150);
        const f = what === 'bad seat' ? 'seat' : 'budget';
        const inp = await rect(p, '#' + f);
        const msg = await rect(p, '#' + f + '-msg');
        const text = await p.textContent('#' + f + '-msg');
        const focused = await p.evaluate(() => document.activeElement.id);
        log(`  ${what}: field ${inp.top}-${inp.bottom}, message ` +
            `${msg.top}-${msg.bottom}, header bottom ${hdr}, screen ` +
            `${hgt}; in view: ${inp.top >= hdr && msg.bottom <= hgt}; ` +
            `focus #${focused}; "${text.slice(0, 40)}…"`);
        if (w === 360 && scheme === 'light') {
          await p.screenshot({ path: `${shots}/${tag}-${f}-error.png` });
        }
      }
      // Same name as an existing card, other case: refused by server.
      await p.fill('#seat', 'SEAT000');
      await p.fill('#budget', '5');
      await p.click('#mint-btn');
      await p.waitForFunction(() =>
        document.querySelector('#seat-msg').textContent);
      log('  SEAT000 (seat000 exists):',
          await p.textContent('#seat-msg'));

      // Mint, copy, then the key box layout.
      const seat = `fix-${w}-${scheme}`;
      await p.fill('#seat', seat);
      await p.fill('#budget', '1');
      const t0 = Date.now();
      await p.click('#mint-btn');
      await p.waitForSelector('#newkey[open]');
      log(`  mint: key box after ${Date.now() - t0} ms`);
      await p.click('#nk-copy');
      await p.waitForTimeout(150);
      const copied = await p.evaluate(async () =>
        (await navigator.clipboard.readText()) ===
          document.querySelector('#nk-key').value &&
        /^sk-/.test(document.querySelector('#nk-key').value));
      log('  Copy key (secure context): button',
          JSON.stringify(await p.textContent('#nk-copy')),
          'clipboard == key:', copied);
      const dlg = await p.evaluate(() => {
        const d = document.querySelector('#newkey');
        const done = document.querySelector('#nk-done')
          .getBoundingClientRect();
        return { content: d.scrollHeight, box: d.clientHeight,
                 doneBottom: Math.round(done.bottom),
                 h: window.innerHeight };
      });
      log(`  key box: content ${dlg.content} in ${dlg.box}, Done bottom ` +
          `${dlg.doneBottom} of ${dlg.h}: visible ${dlg.doneBottom <=
          dlg.h}; model "${await p.textContent('#nk-model')}"`);
      await p.evaluate(() => {
        document.querySelector('#nk-key').value = 'sk-' + '•'.repeat(22);
      });
      await p.screenshot({ path: `${shots}/${tag}-newkey.png` });
      await p.click('#nk-done');
      await p.waitForSelector('#tab-cards:not([hidden])');
      log('  after Done: key wiped', await p.inputValue('#nk-key') === '',
          '| base URL still shown', await p.isVisible('#seat-url'));

      // Cancel a card far down the list: the note stays in its place.
      const victim = `seat${190 + n++}`;
      const card = p.locator('#cards-list article', { has:
        p.locator('h3', { hasText: new RegExp(`^${victim}$`) }) });
      await card.scrollIntoViewIfNeeded();
      await card.locator('button.danger').click();
      await p.waitForTimeout(100);
      log('  focus after Cancel card:', await p.evaluate(() =>
        document.activeElement.textContent));
      if (scheme === 'light' && w === 390) {
        await p.screenshot({ path: `${shots}/${tag}-confirm.png` });
      }
      const before = await card.boundingBox();
      const t1 = Date.now();
      await card.locator('button.solid').click();
      await p.waitForSelector('#cards-list .gone');
      const g = await rect(p, '#cards-list .gone');
      const sy = await p.evaluate(() => Math.round(scrollY));
      log(`  cancel ${victim}: note after ${Date.now() - t1} ms at ` +
          `${g.top}-${g.bottom} (card was ${Math.round(before.y)}-` +
          `${Math.round(before.y + before.height)}), scrollY ${sy}, ` +
          `in view: ${g.top >= 0 && g.bottom <= hgt}; sr text: "` +
          `${await p.textContent('#flash')}"`);
      await p.screenshot({ path: `${shots}/${tag}-after-cancel.png` });
      await p.waitForTimeout(6300);
      log('  note gone after 6 s:',
          !(await p.isVisible('#cards-list .gone')),
          '| card gone:', !(await card.count()));
      // Cancel the card this run made, from the top of the list.
      const mine = p.locator('#cards-list article', { has:
        p.locator('h3', { hasText: new RegExp(`^${seat}$`) }) });
      await mine.locator('button.danger').click();
      await mine.locator('button.solid').click();
      await p.waitForSelector('#cards-list .gone');

      // Tally.
      await p.evaluate(() => window.scrollTo(0, 0));
      await p.click('#tabs a[data-tab=tally]');
      await p.waitForSelector('#tally-models .bar-row');
      await layout(p, 'tally');
      log('  tally models:', JSON.stringify(await p.$$eval(
        '#tally-models .bar-row', (rs) => rs.map((r) => r.innerText
          .replace(/\n+/g, ' | ')))));
      await p.screenshot({ path: `${shots}/${tag}-tally.png` });
      await p.click('.seg button[data-view=all]');
      await p.waitForTimeout(100);
      log('  tally all, tiles:', JSON.stringify(await p.$$eval(
        '#tally-tiles .value', (v) => v.map((x) => x.textContent))));
      log('  errors:', errs.length ? errs : 'none');
      await ctx.close();
    }
  }
  await b.close();
})();
