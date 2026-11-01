// Front Desk stops and starts again under an open page: the pill says
// so, and the next mint works with no reload (fresh CSRF token).
// usage: node restart_fix.js URL SHOTS STOP_CMD START_CMD
const { chromium } = require('playwright');
const { execSync } = require('child_process');
const [url, shots, stopCmd, startCmd] = process.argv.slice(2);
const log = (...a) => console.log(...a);
(async () => {
  const b = await chromium.launch();
  const ctx = await b.newContext({ viewport: { width: 390, height: 844 },
    deviceScaleFactor: 2 });
  const p = await ctx.newPage();
  await p.goto(url);
  await p.waitForSelector('#pill.ok');
  execSync(stopCmd, { shell: '/bin/bash' });
  const t0 = Date.now();
  await p.waitForFunction(() =>
    document.querySelector('#pill').textContent === 'Reconnecting',
    null, { timeout: 15000 });
  log(`stopped: pill "Reconnecting" after ${Date.now() - t0} ms; sub: ` +
      JSON.stringify(await p.textContent('#sub')));
  await p.screenshot({ path: `${shots}/390x844-light-fd-stopped.png` });
  execSync(startCmd, { shell: '/bin/bash' });
  const t1 = Date.now();
  await p.waitForFunction(() =>
    document.querySelector('#pill').textContent === 'Desk open' &&
    /updating live/.test(document.querySelector('#sub').textContent),
    null, { timeout: 20000 });
  log(`restarted: "Desk open" + "updating live" after ${Date.now() - t1}` +
      ' ms');
  await p.click('#tabs a[data-tab=new]');
  await p.fill('#seat', 'restartfix');
  await p.fill('#budget', '1');
  await p.click('#mint-btn');
  const shown = await p.waitForSelector('#newkey[open]', { timeout: 8000 })
    .then(() => true, () => false);
  log('mint right after the restart: key box shown', shown, '| message',
      JSON.stringify(await p.textContent('#mint-msg')));
  if (shown) {
    await p.click('#nk-done');
    const card = p.locator('#cards-list article', { has:
      p.locator('h3', { hasText: /^restartfix$/ }) });
    await card.locator('button.danger').click();
    await card.locator('button.solid').click();
    await p.waitForSelector('#cards-list .gone');
    log('restartfix cancelled');
  }
  await b.close();
})();
