// The original focus.js, as a phone (touch, isMobile), measuring each
// field's own message. usage: node focus2.js URL SHOTS
const { chromium } = require('playwright');
(async () => {
  const [url, dir] = process.argv.slice(2);
  const b = await chromium.launch();
  for (const [w, hgt] of [[360, 740], [390, 844], [360, 640]]) {
    const ctx = await b.newContext({ viewport: { width: w, height: hgt },
      colorScheme: 'light', hasTouch: true, isMobile: true });
    const p = await ctx.newPage();
    await p.goto(url + '#new');
    await p.waitForSelector('#pill.ok');
    await p.waitForSelector('#models input');
    for (const [field, seat, budget, models] of [
        ['seat', 'Sam Smith', '5', true], ['budget', 'ok-name', 'five', true],
        ['models', 'ok-name', '5', false]]) {
      await p.fill('#seat', seat);
      await p.fill('#budget', budget);
      for (const c of await p.$$('#models input')) {
        if ((await c.isChecked()) !== (models && await c.evaluate(
          (x) => x.value === 'local'))) await c.tap();
      }
      await p.evaluate(() => scrollTo(0, 99999));
      await p.tap('#mint-btn');
      await p.waitForTimeout(500);
      const r = await p.evaluate((f) => {
        const box = (s) => { const e = document.querySelector(s)
          .getBoundingClientRect(); return [Math.round(e.top),
          Math.round(e.bottom)]; };
        const hdr = box('.top')[1];
        const inp = f === 'models' ? box('#models') : box('#' + f);
        const msg = box('#' + f + '-msg');
        return { hdr, inp, msg, inner: innerHeight,
          ok: inp[0] >= hdr && msg[1] <= innerHeight,
          focused: document.activeElement.id || document.activeElement.value,
          text: document.querySelector('#' + f + '-msg').textContent };
      }, field);
      console.log(`${w}x${hgt} bad ${field}: ${JSON.stringify(r)}`);
    }
    await p.screenshot({ path: `${dir}/focus2-models-${w}x${hgt}.png` });
    await ctx.close();
  }
  await b.close();
})();
