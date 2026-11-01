// Does a queued dialog 'close' event wipe a key shown right after?
const { chromium } = require('playwright');
(async () => {
  const [url] = process.argv.slice(2);
  const b = await chromium.launch();
  const p = await (await b.newContext({ viewport: { width: 390,
    height: 844 } })).newPage();
  await p.goto(url + '/#cards');
  await p.waitForSelector('#pill.ok');
  const r = { seat: 'demo', key: 'sk-FAKEfake1234567890', base_url:
    'http://x:4000', models: ['local'], budget: '5', duration: '30d' };
  await p.evaluate((r) => showKey(r), r);
  await p.evaluate(() => closeKey());
  await p.evaluate((r) => showKey(r), r);
  await p.waitForTimeout(300);
  console.log('separate evaluates, key kept:',
    await p.inputValue('#nk-key') === r.key);
  await p.evaluate(() => closeKey());
  await p.waitForTimeout(300);
  await p.evaluate((r) => showKey(r), r);
  await p.waitForTimeout(300);
  console.log('300 ms between close and show, key kept:',
    await p.inputValue('#nk-key') === r.key);
  // what a person does: Done, then a mint round trip later
  await p.click('#nk-done');
  await p.click('#tabs a[data-tab=new]');
  await p.evaluate(async (r) => { await new Promise((ok) =>
    setTimeout(ok, 60)); showKey(r); }, r);
  await p.waitForTimeout(300);
  console.log('Done click, 60 ms later show, key kept:',
    await p.inputValue('#nk-key') === r.key);
  await b.close();
})();
