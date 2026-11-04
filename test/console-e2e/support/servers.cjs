// Starts the shadowmock binary the browser tests drive: one plain standalone server, and one central server that asks
// for an admin token. It also starts a small upstream server for the recording scenarios.
'use strict';

const { spawn, execFileSync } = require('node:child_process');
const fs = require('node:fs');
const http = require('node:http');
const net = require('node:net');
const os = require('node:os');
const path = require('node:path');

const repoRoot = path.resolve(__dirname, '..', '..', '..');
const adminToken = 'console-e2e-admin-token';

function freePort() {
  return new Promise((resolve, reject) => {
    const s = net.createServer();
    s.once('error', reject);
    s.listen(0, '127.0.0.1', () => {
      const { port } = s.address();
      s.close(() => resolve(port));
    });
  });
}

function binary(dir) {
  if (process.env.SHADOWMOCK_BIN) return path.resolve(process.env.SHADOWMOCK_BIN);
  const out = path.join(dir, process.platform === 'win32' ? 'shadowmock.exe' : 'shadowmock');
  execFileSync('go', ['build', '-o', out, './cmd/shadowmock'], { cwd: repoRoot, stdio: 'inherit' });
  return out;
}

async function waitFor(url, child) {
  for (let i = 0; i < 100; i++) {
    if (child.exitCode !== null) throw new Error(`shadowmock exited with ${child.exitCode} before it was ready`);
    try {
      const res = await fetch(url);
      if (res.ok) return;
    } catch (e) { /* not listening yet */ }
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error(`${url} did not become ready`);
}

async function start(bin, dir, name, yaml) {
  const port = await freePort();
  const cfg = path.join(dir, `${name}.yaml`);
  fs.writeFileSync(cfg, yaml);
  const child = spawn(bin, ['--config', cfg, '--port', String(port)], { cwd: dir, stdio: ['ignore', 'inherit', 'inherit'] });
  await waitFor(`http://127.0.0.1:${port}/__admin/health`, child);
  return { child, port, url: `http://127.0.0.1:${port}` };
}

// startUpstream serves canned answers for the recording scenarios.
function startUpstream() {
  return new Promise((resolve) => {
    const server = http.createServer((req, res) => {
      res.setHeader('Content-Type', 'application/json');
      res.end(JSON.stringify({ id: 1, path: req.url }));
    });
    server.listen(0, '127.0.0.1', () => resolve({ server, url: `http://127.0.0.1:${server.address().port}` }));
  });
}

async function startAll() {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'shadowmock-console-e2e-'));
  const bin = binary(dir);
  const plain = await start(bin, dir, 'plain', 'console: { enabled: true }\n');
  const secured = await start(bin, dir, 'secured', [
    'mode: server',
    'console: { enabled: true }',
    'auth:',
    '  driver: token',
    '  tokens:',
    `    - { name: ops, role: admin, token: "${adminToken}" }`,
    '',
  ].join('\n'));
  const upstream = await startUpstream();
  return { dir, plain, secured, upstream, adminToken };
}

async function stopAll(s) {
  if (!s) return;
  const exits = [s.plain, s.secured].map((x) => new Promise((resolve) => {
    if (x.child.exitCode !== null) return resolve();
    x.child.once('exit', resolve);
    x.child.kill();
  }));
  await Promise.all(exits);
  s.upstream.server.close();
  try {
    fs.rmSync(s.dir, { recursive: true, force: true, maxRetries: 10, retryDelay: 200 });
  } catch (e) { /* a temp directory left behind is harmless */ }
}

module.exports = { startAll, stopAll };
