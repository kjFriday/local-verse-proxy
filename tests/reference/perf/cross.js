// A card made in Front Desk (by API): how soon another open page shows
// it, and how soon it disappears after Cancel.
const { chromium } = require('playwright');
const http = require('http');
const [url] = process.argv.slice(2);
const u = new URL(url);
function post(path, body, csrf) {
  return new Promise((ok, no) => {
    const data = JSON.stringify(body);
    const r = http.request({ host: u.hostname, port: u.port, path,
      method: 'POST', headers: { 'Content-Type': 'application/json',
      'X-CSRF-Token': csrf, Origin: url, 'Content-Length': data.length } },
      (res) => { let b = ''; res.on('data', (c) => b += c);
                 res.on('end', () => ok([res.statusCode, b.length])); });
    r.on('error', no); r.end(data);
  });
}
(async () => {
  const b = await chromium.launch();
  const p = await (await b.newContext()).newPage();
  await p.goto(url);
  await p.waitForSelector('#pill.ok');
  const csrf = await p.evaluate(() => csrf);
  const add = [], del = [];
  for (let i = 0; i < 5; i++) {
    const seat = 'cross-' + i;
    let t = Date.now();
    const [code] = await post('/api/mint', { seat, models: ['local'],
      budget: '1', duration: '1h' }, csrf);
    await p.waitForFunction((s) => state.cards.some((c) => c.seat === s),
                            seat);
    add.push(Date.now() - t);
    t = Date.now();
    await post('/api/revoke', { seat }, csrf);
    await p.waitForFunction((s) => !state.cards.some((c) => c.seat === s),
                            seat);
    del.push(Date.now() - t);
    await p.waitForTimeout(700);
  }
  console.log('mint request start -> card on another page (ms):',
              add.join(', '));
  console.log('revoke request start -> card gone there (ms):',
              del.join(', '));
  await b.close();
})();
