// A card made in the terminal: how long until an open page shows it.
const { chromium } = require('playwright');
const { execSync } = require('child_process');
const [url, repo] = process.argv.slice(2);
(async () => {
  const b = await chromium.launch();
  const p = await (await b.newContext()).newPage();
  await p.goto(url);
  await p.waitForSelector('#pill.ok');
  const xs = [];
  for (let i = 0; i < 5; i++) {
    const seat = 'term-' + i;
    execSync(`./mint-key.sh ${seat} --models local --budget 1 > /dev/null`,
             { cwd: repo });
    const t = Date.now();
    await p.waitForFunction((s) => state.cards.some((c) => c.seat === s),
                            seat, { timeout: 20000 });
    xs.push(Date.now() - t);
    execSync(`./mint-key.sh --revoke-seat ${seat} > /dev/null`,
             { cwd: repo });
    await p.waitForTimeout(1500 + i * 1700);
  }
  xs.sort((a, b) => a - b);
  console.log('terminal mint -> card on an open page (ms):', xs.join(', '));
  await b.close();
})();
