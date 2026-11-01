// Proxy stopped under an open page; then started again.
const { chromium } = require('playwright');
const { execSync } = require('child_process');
const [url, shot, stopCmd, startCmd] = process.argv.slice(2);
(async () => {
  const b = await chromium.launch();
  const p = await (await b.newContext({ viewport: { width: 390,
    height: 844 }, deviceScaleFactor: 2 })).newPage();
  await p.goto(url + '/#cards');
  await p.waitForSelector('#pill.ok');
  execSync(stopCmd, { shell: '/bin/bash' });
  const t = Date.now();
  await p.waitForSelector('#pill.down', { timeout: 30000 });
  console.log('proxy down: pill', await p.textContent('#pill'), 'after',
    Date.now() - t, 'ms; note:', JSON.stringify(
      (await p.textContent('#notes')).slice(0, 160)));
  console.log('cards still listed:', await p.$$eval('#cards-list article',
    (a) => a.length));
  await p.screenshot({ path: shot });
  await p.click('#tabs a[data-tab=new]');
  await p.fill('#seat', 'whiledown');
  await p.click('#mint-btn');
  await p.waitForFunction(() => document.querySelector('#mint-msg')
    .textContent || document.querySelector('#seat-msg').textContent);
  console.log('mint while proxy down:', JSON.stringify(
    await p.textContent('#mint-msg')));
  execSync(startCmd, { shell: '/bin/bash' });
  const t2 = Date.now();
  await p.waitForSelector('#pill.ok', { timeout: 120000 });
  console.log('proxy back: "Desk open" after', Date.now() - t2, 'ms');
  await b.close();
})();
