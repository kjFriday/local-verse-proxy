// Chromium: page load to 200 cards, render() cost, desktop and 4x CPU
// throttle (a mid phone). usage: node browser.js URL shotsdir
const { chromium } = require("playwright");
const [url, shots] = process.argv.slice(2);
const pct = (xs) => { xs = xs.slice().sort((a, b) => a - b);
  return `p50 ${xs[xs.length >> 1].toFixed(1)} p95 ${xs[Math.min(xs.length - 1,
    Math.floor(xs.length * .95))].toFixed(1)} ms n=${xs.length}`; };
(async () => {
  const b = await chromium.launch();
  for (const throttle of [1, 4]) {
    const loads = [];
    for (let i = 0; i < 8; i++) {
      const ctx = await b.newContext({ viewport: { width: 390, height: 844 } });
      const page = await ctx.newPage();
      const cdp = await ctx.newCDPSession(page);
      await cdp.send("Emulation.setCPUThrottlingRate", { rate: throttle });
      const t = Date.now();
      await page.goto(url);
      await page.waitForFunction(() =>
        document.querySelectorAll("#cards-list article").length >= 200);
      const nav = await page.evaluate(() => {
        const n = performance.getEntriesByType("navigation")[0];
        return { ttfb: n.responseStart, dcl: n.domContentLoadedEventEnd,
                 cards: performance.now() };
      });
      loads.push(nav);
      if (i === 7) {
        // render() cost: parse + rebuild everything, as on each push
        const r = await page.evaluate(async () => {
          const res = await fetch("/api/state"); const txt = await res.text();
          const p = [], q = [], rc = [], rt = [];
          for (let k = 0; k < 20; k++) {
            let a = performance.now(); const s = JSON.parse(txt);
            p.push(performance.now() - a);
            a = performance.now(); render(s); q.push(performance.now() - a);
            a = performance.now(); renderCards(); rc.push(performance.now() - a);
            a = performance.now(); renderTally(); rt.push(performance.now() - a);
            await new Promise((ok) => requestAnimationFrame(() => setTimeout(ok)));
          }
          return { bytes: txt.length, p, q, rc, rt,
                   nodes: document.getElementsByTagName("*").length };
        });
        // Layout+style after a full re-render, the part render() hides
        const lay = await page.evaluate(async () => {
          const xs = [];
          for (let k = 0; k < 10; k++) {
            render(state);
            const a = performance.now();
            document.body.getBoundingClientRect(); void document.body.offsetHeight;
            xs.push(performance.now() - a);
            await new Promise((ok) => setTimeout(ok, 30));
          }
          return xs;
        });
        console.log(`[cpu x${throttle}] state ${r.bytes} B, DOM nodes ${r.nodes}`);
        console.log(`[cpu x${throttle}] JSON.parse     ${pct(r.p)}`);
        console.log(`[cpu x${throttle}] render() all   ${pct(r.q)}`);
        console.log(`[cpu x${throttle}]   renderCards  ${pct(r.rc)}`);
        console.log(`[cpu x${throttle}]   renderTally  ${pct(r.rt)}`);
        console.log(`[cpu x${throttle}] forced layout  ${pct(lay)}`);
        if (throttle === 1) await page.screenshot({ path: shots + "/390-200cards.png" });
      }
      await ctx.close();
    }
    console.log(`[cpu x${throttle}] TTFB ${pct(loads.map((x) => x.ttfb))}`);
    console.log(`[cpu x${throttle}] DOMContentLoaded ${pct(loads.map((x) => x.dcl))}`);
    console.log(`[cpu x${throttle}] 200 cards drawn ${pct(loads.map((x) => x.cards))}`);
  }
  await b.close();
})();
