// Preloaded (node --require) into the reference scripts that open
// the page without logging in, when the desk has a password (--docker
// always has one): it logs in once through /api/login and hands the
// cookie to each new browser context (context.addCookies), so the
// scripts run unchanged. The scripts themselves are not edited.
//
// E2E_URL is the desk, E2E_PWFILE its password. With E2E_START set (a
// script that stops and starts the desk), a desk that comes back has
// forgotten its sessions, as lan_restart.js shows: once the start
// command has run, this logs in again before the page's next request
// leaves (every request of the context waits on a route until then),
// as the person would on the login screen, and sends the new cookie.
'use strict';
const cp = require('child_process');
const pw = require('playwright');

const url = process.env.E2E_URL;
const start = process.env.E2E_START || '';

// One login in a child node (this one may not wait on a promise):
// prints name=value of the session cookie, or fails.
const child = `
const http = require('http');
const fs = require('fs');
const u = new URL(process.env.E2E_URL);
const body = JSON.stringify({ password:
  fs.readFileSync(process.env.E2E_PWFILE, 'utf8').trim() });
const r = http.request({ host: u.hostname, port: u.port,
  path: '/api/login', method: 'POST', headers: {
    'Content-Type': 'application/json',
    'Content-Length': Buffer.byteLength(body) } }, (res) => {
  res.resume();
  const c = (res.headers['set-cookie'] || [])[0] || '';
  if (res.statusCode !== 200 || !c) process.exit(1);
  process.stdout.write(c.split(';')[0]);
});
r.on('error', () => process.exit(1));
r.end(body);
`;

function loginSync(waitMs) {
  const env = Object.assign({}, process.env);
  delete env.NODE_OPTIONS;
  const until = Date.now() + waitMs;
  for (;;) {
    try {
      return cp.execFileSync(process.execPath, ['-e', child],
        { env, encoding: 'utf8', timeout: 10000 });
    } catch (e) {
      if (Date.now() > until) throw new Error('login: no session');
      Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 250);
    }
  }
}

function asCookie(nv) {
  const i = nv.indexOf('=');
  return { name: nv.slice(0, i), value: nv.slice(i + 1), url,
    httpOnly: true, sameSite: 'Strict' };
}

let current = loginSync(5000);
let fresh = false;
const contexts = [];

const execSync = cp.execSync;
cp.execSync = function (cmd, ...rest) {
  const out = execSync.call(this, cmd, ...rest);
  if (start && cmd === start) {
    current = loginSync(30000);
    fresh = true;
  }
  return out;
};

async function prepare(ctx) {
  await ctx.addCookies([asCookie(current)]);
  contexts.push(ctx);
  if (!start) return ctx;
  await ctx.route('**/*', async (route) => {
    if (fresh) {
      fresh = false;
      for (const c of contexts) await c.addCookies([asCookie(current)]);
    }
    const h = Object.assign({}, route.request().headers(),
      { cookie: current });
    await route.continue({ headers: h });
  });
  return ctx;
}

const launch = pw.chromium.launch.bind(pw.chromium);
pw.chromium.launch = async (...a) => {
  const b = await launch(...a);
  const newContext = b.newContext.bind(b);
  b.newContext = async (...o) => prepare(await newContext(...o));
  b.newPage = async (...o) => (await b.newContext(...o)).newPage();
  return b;
};
