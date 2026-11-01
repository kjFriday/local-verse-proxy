// --lan restart under an open, logged-in page: does it ask to log in?
const { chromium } = require('playwright');
const { execSync } = require('child_process');
const fs = require('fs');
const [url, pwfile, stopCmd, startCmd] = process.argv.slice(2);
(async () => {
  const b = await chromium.launch();
  const p = await (await b.newContext({ viewport: { width: 390,
    height: 844 } })).newPage();
  await p.goto(url);
  await p.waitForSelector('#login:not([hidden])');
  await p.fill('#pw', fs.readFileSync(pwfile, 'utf8').trim());
  await p.click('#login-form button');
  await p.waitForSelector('#pill.ok');
  execSync(stopCmd, { shell: '/bin/bash' });
  await p.waitForTimeout(4000);
  console.log('stopped: pill', await p.textContent('#pill'));
  execSync(startCmd, { shell: '/bin/bash' });
  const t = Date.now();
  const shown = await p.waitForSelector('#login:not([hidden])',
    { timeout: 20000 }).then(() => true, () => false);
  console.log('after restart: login asked', shown, 'after', Date.now() - t,
    'ms; pill', await p.textContent('#pill'));
  await b.close();
})();
