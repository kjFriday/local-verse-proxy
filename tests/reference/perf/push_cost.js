// Main-thread cost of one realistic push: 100 of 200 cards changed
// (as measured under traffic), page on the cards tab, phone-like CPU.
// usage: node push_cost.js URL throttle
const { chromium } = require("playwright");
const [url, rate] = [process.argv[2], +process.argv[3]];
const pct = (xs) => { xs = xs.slice().sort((a, b) => a - b);
  return `p50 ${xs[xs.length >> 1].toFixed(1)} p95 ${xs[Math.min(xs.length - 1,
    Math.floor(xs.length * .95))].toFixed(1)} ms`; };
(async () => {
  const b = await chromium.launch();
  const ctx = await b.newContext({ viewport: { width: 390, height: 844 } });
  const page = await ctx.newPage();
  const cdp = await ctx.newCDPSession(page);
  await page.goto(url);
  await page.waitForFunction(() =>
    document.querySelectorAll("#cards-list article").length >= 200);
  await cdp.send("Emulation.setCPUThrottlingRate", { rate });
  const r = await page.evaluate(async () => {
    const base = JSON.parse(JSON.stringify(state));
    const js = [], all = [];
    for (let k = 0; k < 20; k++) {
      const s = JSON.parse(JSON.stringify(base));
      // half the cards get new spend/used, the tally changes too
      s.cards.forEach((c, i) => { if (i % 2 === k % 2) {
        c.spent += 0.001 * (k + 1); c.used = new Date().toISOString(); } });
      if (s.tally) s.tally.seats.forEach((r) => { r.t_reqs += k + 1; });
      const a = performance.now();
      render(s);
      const mid = performance.now();
      document.body.getBoundingClientRect(); void document.body.offsetHeight;
      const end = performance.now();
      js.push(mid - a); all.push(end - a);
      await new Promise((ok) => setTimeout(ok, 50));
    }
    return { js, all };
  });
  console.log(`${url} cpu x${rate}: render() ${pct(r.js)}; render+style+layout ${pct(r.all)}`);
  await b.close();
})();
