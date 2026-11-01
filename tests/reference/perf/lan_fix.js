// --lan over plain http (not a secure context): copy buttons in the
// open key box, login messages, key box on small screens. Keys are
// compared inside Node and never printed.
// usage: node lan_fix.js LAN_URL LOCAL_URL PWFILE SHOTS
const { chromium } = require('playwright');
const fs = require('fs');
const [url, local, pwfile, shots] = process.argv.slice(2);
const pw = fs.readFileSync(pwfile, 'utf8').trim();
const log = (...a) => console.log(...a);
(async () => {
  const b = await chromium.launch();
  const ctx = await b.newContext({ viewport: { width: 390, height: 844 },
    colorScheme: 'dark', deviceScaleFactor: 2 });
  await ctx.grantPermissions(['clipboard-read', 'clipboard-write'],
                             { origin: local });
  const p = await ctx.newPage();
  const q = await ctx.newPage();  // a secure page, to read the clipboard
  await q.goto(local);
  const read = () => q.evaluate(() => navigator.clipboard.readText());
  const put = (t) => q.evaluate((t) => navigator.clipboard.writeText(t), t);
  const errs = [];
  p.on('pageerror', (e) => errs.push(String(e)));
  await p.goto(url);
  log('secure context:', await p.evaluate(() => window.isSecureContext));
  await p.waitForSelector('#login:not([hidden])');
  log('plain-http warning shown:', await p.isVisible('#plain-http'));
  await p.fill('#pw', 'not-the-password');
  await p.click('#login-form button');
  await p.waitForFunction(() =>
    document.querySelector('#login-msg').textContent);
  log('wrong password says:', await p.textContent('#login-msg'));
  await p.screenshot({ path: `${shots}/lan-390x844-dark-login.png` });
  await p.fill('#pw', pw);
  await p.click('#login-form button');
  await p.waitForSelector('#pill.ok');
  log('after login: base URL shown', await p.inputValue('#seat-url'));
  await p.screenshot({ path: `${shots}/lan-390x844-dark-cards.png` });

  // Mint through the page, then both Copy buttons.
  await p.click('#tabs a[data-tab=new]');
  await p.waitForSelector('#models input');
  await p.fill('#seat', 'lanfix');
  await p.fill('#budget', '1');
  await p.click('#mint-btn');
  await p.waitForSelector('#newkey[open]');
  const key = await p.inputValue('#nk-key');
  const base = await p.inputValue('#nk-url');
  await put('BEFORE');
  await p.click('#nk-copy');
  await p.waitForTimeout(200);
  log('Copy key: button', JSON.stringify(await p.textContent('#nk-copy')),
      '| clipboard == key:', (await read()) === key,
      '| key looks like sk-:', /^sk-/.test(key));
  await put('BEFORE');
  await p.click('#nk-copy-url');
  await p.waitForTimeout(200);
  log('Copy base URL: button', JSON.stringify(
    await p.textContent('#nk-copy-url')), '| clipboard == URL:',
      (await read()) === base, base);
  // If the selection copy fails, the page must not claim success.
  await p.evaluate(() => { document.execCommand = () => false; });
  await put('BEFORE');
  await p.click('#nk-copy');
  await p.waitForTimeout(200);
  const sel = await p.evaluate(() => {
    const k = document.querySelector('#nk-key');
    return document.activeElement === k &&
      k.selectionEnd - k.selectionStart === k.value.length;
  });
  log('copy refused by browser: button', JSON.stringify(
    await p.textContent('#nk-copy')), '| key selected for the user:', sel,
      '| clipboard untouched:', (await read()) === 'BEFORE');
  await p.evaluate(() => {
    document.querySelector('#nk-key').value = 'sk-' + '•'.repeat(22);
  });
  await p.screenshot({ path: `${shots}/lan-390x844-dark-copy-refused.png` });
  await p.click('#nk-done');
  await p.waitForSelector('#tab-cards:not([hidden])');

  // Key box on small screens: Done stays reachable.
  for (const [w, hgt] of [[360, 740], [360, 640]]) {
    await p.setViewportSize({ width: w, height: hgt });
    await p.evaluate(() => showKey({ seat: 'demo', key: 'sk-' +
      '•'.repeat(22), base_url: 'http://192.168.1.20:4000',
      models: ['local', 'claude'], budget: '5', duration: '30d' }));
    const r = await p.evaluate(() => {
      const d = document.querySelector('#newkey');
      const done = document.querySelector('#nk-done')
        .getBoundingClientRect();
      return { content: d.scrollHeight, box: d.clientHeight,
               bottom: Math.round(done.bottom), h: innerHeight };
    });
    log(`key box ${w}x${hgt}: content ${r.content} in ${r.box}; Done ` +
        `bottom ${r.bottom} of ${r.h}: visible ${r.bottom <= r.h}`);
    await p.screenshot({ path: `${shots}/lan-${w}x${hgt}-dark-newkey.png` });
    await p.evaluate(() => closeKey());
  }
  await p.setViewportSize({ width: 390, height: 844 });

  // Cancel lanfix through the page.
  const card = p.locator('#cards-list article', { has:
    p.locator('h3', { hasText: /^lanfix$/ }) });
  await card.locator('button.danger').click();
  await card.locator('button.solid').click();
  await p.waitForSelector('#cards-list .gone');
  log('lanfix cancelled through the page');
  log('page errors:', errs.length ? errs : 'none');
  await b.close();
})();
