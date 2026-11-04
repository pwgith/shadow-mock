// The world each scenario gets: a fresh browser page, the engine's URLs and small helpers over its admin API.
'use strict';

const { World, setWorldConstructor, BeforeAll, AfterAll, Before, After, setDefaultTimeout } = require('@cucumber/cucumber');
const { chromium } = require('playwright');
const { startAll, stopAll } = require('./servers.cjs');

setDefaultTimeout(30 * 1000);

let servers;
let browser;

BeforeAll({ timeout: 180 * 1000 }, async function () {
  servers = await startAll();
  browser = await chromium.launch({ headless: !process.env.HEADED });
});

AfterAll(async function () {
  if (browser) await browser.close();
  await stopAll(servers);
});

class ConsoleWorld extends World {
  constructor(options) {
    super(options);
    this.sessions = {}; // name -> { id, key }
    this.dialogs = []; // messages of the confirm boxes the page opened
    this.target = servers.plain;
    this.token = '';
  }

  get upstream() { return servers.upstream.url; }
  get adminToken() { return servers.adminToken; }
  get consoleUrl() { return `${this.target.url}/__console/`; }

  useSecuredServer() { this.target = servers.secured; this.token = servers.adminToken; }

  // admin calls the admin API as a test would, outside the browser.
  async admin(method, path, body) {
    const headers = {};
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    if (this.token) headers.Authorization = `Bearer ${this.token}`;
    const res = await fetch(`${this.target.url}/__admin${path}`, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
    const text = await res.text();
    let json = null;
    try { json = text ? JSON.parse(text) : null; } catch (e) { /* not JSON */ }
    if (!res.ok) throw new Error(`${method} ${path} answered ${res.status}: ${text}`);
    return json;
  }

  // mock sends a request to the mock side, as the system under test would.
  async mock(method, path, headers) {
    return fetch(`${this.target.url}${path}`, { method, headers });
  }

  // expand replaces ${Name.id} and ${Name.key} with the shadow mock's values.
  expand(text) {
    return text.replace(/\$\{(\w+)\.(id|key)\}/g, (_, name, field) => {
      const s = this.sessions[name];
      if (!s) throw new Error(`no shadow mock called ${name}`);
      return s[field];
    });
  }

  async reset() {
    await this.admin('POST', '/reset');
    const { sessions } = await this.admin('GET', '/sessions');
    for (const s of sessions) await this.admin('DELETE', `/sessions/${s.id}`);
  }
}

setWorldConstructor(ConsoleWorld);

Before(async function () {
  await this.reset();
  this.context = await browser.newContext();
  this.page = await this.context.newPage();
  this.errors = [];
  this.page.on('pageerror', (e) => this.errors.push(e.message));
  // The console asks before it deletes or resets; a scenario answers through this.confirmWith.
  this.confirmWith = true;
  this.page.on('dialog', async (d) => {
    this.dialogs.push(d.message());
    if (this.confirmWith) await d.accept(); else await d.dismiss();
  });
});

After(async function (scenario) {
  if (scenario.result && scenario.result.status === 'FAILED' && this.page) {
    this.attach(await this.page.screenshot({ fullPage: true }), 'image/png');
  }
  if (this.context) await this.context.close();
  // The secured server is not reset before a scenario, so clear what the scenario left in it.
  if (this.target !== servers.plain) await this.reset();
});
