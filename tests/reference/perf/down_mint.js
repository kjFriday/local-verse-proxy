// Mint while Front Desk is down; then restart: what does the page say?
const { chromium } = require('playwright');
const { execSync } = require('child_process');
const [url, stopCmd, startCmd] = process.argv.slice(2);
(async () => {
  const b = await chromium.launch();
  const p = await (await b.newContext({ viewport: { width: 390,
    height: 844 } })).newPage();
  await p.goto(url + '/#new');
  await p.waitForSelector('#pill.ok');
  await p.waitForSelector('#models input');
  execSync(stopCmd, { shell: '/bin/bash' });
  await p.waitForTimeout(4000);
  console.log('down: pill', await p.textContent('#pill'), '| sub',
              await p.textContent('#sub'));
  await p.fill('#seat', 'downmint');
  await p.fill('#budget', '1');
  await p.click('#mint-btn');
  await p.waitForTimeout(800);
  console.log('mint while down says:', JSON.stringify(
    await p.textContent('#mint-msg')), '| btn',
    JSON.stringify(await p.textContent('#mint-btn')));
  execSync(startCmd, { shell: '/bin/bash' });
  await p.waitForFunction(() => /updating live/.test(
    document.getElementById('sub').textContent), null, { timeout: 30000 });
  await p.click('#mint-btn');
  const ok = await p.waitForSelector('#newkey[open]', { timeout: 8000 })
    .then(() => true, () => false);
  console.log('mint after restart (same form, no reload): key box', ok,
    '| msg', JSON.stringify(await p.textContent('#mint-msg')));
  if (ok) {
    await p.click('#nk-done');
    const card = p.locator('#cards-list article', { has:
      p.locator('h3', { hasText: /^downmint$/ }) });
    await card.locator('button.danger').click();
    await card.locator('button.solid').click();
    await p.waitForSelector('#cards-list .gone');
    console.log('downmint cancelled');
  }
  await b.close();
})();
