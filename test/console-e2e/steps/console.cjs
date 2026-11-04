// Steps for the console features. They act on the page the way a person would (click, type, read what is shown) and
// check the engine only through its admin API, so a passing scenario means the page and the API agree.
'use strict';

const assert = require('node:assert/strict');
const { Given, When, Then } = require('@cucumber/cucumber');

const row = (world, selector, text) => world.page.locator(selector, { hasText: text });

// ---- Setting up the engine ----------------------------------------------------------------------------------

Given('the global mapping:', async function (doc) {
  await this.admin('POST', '/mappings', JSON.parse(doc));
});

Given('a key-based shadow mock {string}', async function (name) { await createSession(this, 'key-based', name, '{}'); });
Given('a key-based shadow mock {string} with:', async function (name, doc) { await createSession(this, 'key-based', name, doc); });
Given('a URL-based shadow mock {string}', async function (name) { await createSession(this, 'url-based', name, '{}'); });
Given('a URL-based shadow mock {string} with:', async function (name, doc) { await createSession(this, 'url-based', name, doc); });

async function createSession(world, kind, name, doc) {
  const body = { name, ...JSON.parse(world.expand(doc)) };
  const made = await world.admin('POST', `/sessions/${kind}`, body);
  world.sessions[name] = { id: made.id, key: made.key };
}

Given('the mapping in {string}:', async function (name, doc) {
  await this.admin('POST', `/sessions/${this.sessions[name].id}/mappings`, JSON.parse(doc));
});

Given('the central server asks for an admin token', function () { this.useSecuredServer(); });

// A request as the system under test would send it. The header is optional.
When(/^I send (?:a|an) "([^"]*)" request to "([^"]*)"(?: with header "([^"]*)" set to "([^"]*)")?$/, async function (method, path, header, value) {
  const headers = header ? { [header]: this.expand(value) } : undefined;
  this.last = await this.mock(method, path, headers);
});

Then('the mock answers with status {int}', function (status) { assert.equal(this.last.status, status); });

When(/^I send (\d+) "([^"]*)" requests to "([^"]*)"$/, async function (n, method, path) {
  for (let i = 0; i < Number(n); i++) this.last = await this.mock(method, path);
});

// ---- Opening the console ------------------------------------------------------------------------------------

Given('I open the console', async function () {
  await this.page.goto(this.consoleUrl);
  // The journal is the first tab and loads once the page has fetched calls.json. Either entries, the empty note or the
  // server's refusal show.
  await this.page.locator('#journal-list li, #status.error').first().waitFor();
});

Given('I open the console and see no requests yet', async function () {
  await this.page.goto(this.consoleUrl);
  await this.page.locator('#journal-list li', { hasText: 'No requests.' }).waitFor();
});

When('I open the {string} tab', async function (tab) {
  await this.page.locator('nav button', { hasText: new RegExp(`^${tab}$`) }).click();
  await this.page.locator('nav button[aria-pressed="true"]', { hasText: new RegExp(`^${tab}$`) }).waitFor();
});

Then('the {string} tab is showing', async function (tab) {
  await this.page.locator('nav button[aria-pressed="true"]', { hasText: new RegExp(`^${tab}$`) }).waitFor();
});

Then('the page header says {string}', async function (text) {
  await this.page.locator('h1', { hasText: text }).waitFor();
});

Then('the page loaded no script or style from another origin', async function () {
  const external = await this.page.evaluate(() =>
    [...document.querySelectorAll('script[src], link[href], img[src]')]
      .map((e) => e.src || e.href)
      .filter((u) => new URL(u, location.href).origin !== location.origin));
  assert.deepEqual(external, []);
});

Then('the page has no script errors', function () { assert.deepEqual(this.errors, []); });

// ---- Journal ------------------------------------------------------------------------------------------------

When('I refresh the journal', async function () {
  await this.page.locator('#refresh-journal').click();
});

When('I show only unmatched requests', async function () {
  await this.page.locator('#unmatched-only').check();
});

When('I select the journal entry {string}', async function (text) {
  await row(this, '#journal-list li', text).click();
  await this.page.locator('#journal-list li[aria-selected="true"]', { hasText: text }).waitFor();
});

Then('the journal lists {string}', async function (text) {
  await row(this, '#journal-list li', text).first().waitFor();
});

Then('the journal does not list {string}', async function (text) {
  await row(this, '#journal-list li', text).first().waitFor({ state: 'detached' });
});

Then('the journal entry {string} is marked {string}', async function (text, mark) {
  await this.page.locator('#journal-list li', { hasText: text }).locator('span', { hasText: new RegExp(`^${mark}$`) }).waitFor();
});

Then('the journal detail shows {string}', async function (text) {
  await this.page.locator('#journal-detail').getByText(text).first().waitFor();
});

Then('the journal detail does not show {string}', async function (text) {
  assert.equal(await this.page.locator('#journal-detail').getByText(text).count(), 0);
});

Then('the journal detail holds no image or script element', async function () {
  assert.equal(await this.page.locator('#journal-detail img, #journal-detail script, #journal-list img, #journal-list script').count(), 0);
  assert.equal(await this.page.evaluate(() => window.pwned), undefined);
});

// ---- Shadow mocks -------------------------------------------------------------------------------------------

Then('the console lists the shadow mock {string}', async function (name) {
  await row(this, '#sessions-body tr', name).first().waitFor();
});

Then('the console does not list the shadow mock {string}', async function (name) {
  await row(this, '#sessions-body tr', name).first().waitFor({ state: 'detached' });
});

Then('the shadow mock {string} is shown as {string}', async function (name, kind) {
  await row(this, '#sessions-body tr', name).locator('td', { hasText: kind }).first().waitFor();
});

Then('the shadow mock {string} is not marked as having no global fallback', async function (name) {
  const r = row(this, '#sessions-body tr', name);
  await r.first().waitFor();
  assert.equal(await r.locator('td', { hasText: 'no global fallback' }).count(), 0);
});

Then('the console says there are no shadow mocks', async function () {
  await this.page.locator('#sessions-empty').waitFor({ state: 'visible' });
});

Given('I will decline the confirmation', function () { this.confirmWith = false; });

When('I delete the shadow mock {string}', async function (name) {
  await row(this, '#sessions-body tr', name).getByRole('button', { name: 'Delete' }).click();
});

Then('I was asked {string}', function (message) { assert.ok(this.dialogs.includes(message), `asked ${JSON.stringify(this.dialogs)}`); });

When('I edit the shadow mock {string}', async function (name) {
  await row(this, '#sessions-body tr', name).getByRole('button', { name: 'Edit' }).click();
  await this.page.locator('#session-edit').getByRole('heading', { name: `Edit ${name}` }).waitFor();
});

When('I rename it to {string}', async function (newName) {
  const panel = this.page.locator('#session-edit');
  await panel.getByLabel('Name').fill(newName);
  await panel.getByRole('button', { name: 'Save' }).click();
  await panel.getByRole('heading', { name: `Edit ${newName}` }).waitFor();
});

When('I set its lifetime to {int} seconds', async function (seconds) {
  const panel = this.page.locator('#session-edit');
  await panel.getByLabel('Lifetime from now, in seconds').fill(String(seconds));
  await panel.getByRole('button', { name: 'Save' }).click();
});

When('I save without changing anything', async function () {
  await this.page.locator('#session-edit').getByRole('button', { name: 'Save' }).click();
});

When('I add the match token {string}', async function (token) {
  const form = this.page.locator('#session-edit form', { has: this.page.getByLabel('New token') });
  await form.getByLabel('New token').fill(token);
  await form.getByRole('button', { name: 'Add' }).click();
  await this.page.locator('#session-edit .chips li', { hasText: token }).first().waitFor();
});

When('I remove the match token {string}', async function (token) {
  await this.page.locator('#session-edit .chips li', { hasText: token }).getByRole('button', { name: 'Remove' }).click();
  await this.page.locator('#session-edit .chips li', { hasText: token }).waitFor({ state: 'detached' });
});

When('I hide the mapping with id {string}', async function (id) {
  const form = this.page.locator('#session-edit form', { has: this.page.getByLabel('Hide the mapping with id') });
  await form.getByLabel('Hide the mapping with id').fill(id);
  await form.getByRole('button', { name: 'Add' }).click();
  await this.page.locator('#session-edit .chips li', { hasText: `mapping ${id}` }).waitFor();
});

When('I stop hiding the mapping with id {string}', async function (id) {
  await this.page.locator('#session-edit .chips li', { hasText: `mapping ${id}` }).getByRole('button', { name: 'Remove' }).click();
  await this.page.locator('#session-edit .chips li', { hasText: `mapping ${id}` }).waitFor({ state: 'detached' });
});

When('I keep it alive', async function () {
  await this.page.locator('#session-edit').getByRole('button', { name: 'Keep alive' }).click();
});

When('I close the editor', async function () {
  await this.page.locator('#session-edit').getByRole('button', { name: 'Close' }).click();
});

Then('the editor is closed', async function () { await this.page.locator('#session-edit').waitFor({ state: 'hidden' }); });

Then('the editor shows {string}', async function (text) {
  await this.page.locator('#session-edit').getByText(text).first().waitFor();
});

Then('the editor offers no match tokens', async function () {
  assert.equal(await this.page.locator('#session-edit').getByText('Match tokens').count(), 0);
});

Then('the status line says {string}', async function (text) {
  await this.page.locator('#status', { hasText: text }).waitFor();
});

Then('the status line shows no error', async function () {
  assert.equal(await this.page.locator('#status.error').count(), 0);
});

Then('the admin API shows the shadow mock {string} with name {string}', async function (name, want) {
  const got = await this.admin('GET', `/sessions/${this.sessions[name].id}`);
  assert.equal(got.name, want);
});

Then('the admin API shows the shadow mock {string} with token {string}', async function (name, token) {
  const got = await this.admin('GET', `/sessions/${this.sessions[name].id}`);
  assert.ok((got.tokens || []).includes(token), `tokens ${JSON.stringify(got.tokens)}`);
});

Then('the admin API shows the shadow mock {string} without token {string}', async function (name, token) {
  const got = await this.admin('GET', `/sessions/${this.sessions[name].id}`);
  assert.ok(!(got.tokens || []).includes(token), `tokens ${JSON.stringify(got.tokens)}`);
});

Then('the admin API shows the shadow mock {string} hiding the mapping {string}', async function (name, id) {
  const got = await this.admin('GET', `/sessions/${this.sessions[name].id}`);
  assert.ok((got.masks || []).some((m) => m.id === id), `masks ${JSON.stringify(got.masks)}`);
});

Then('the admin API shows the shadow mock {string} living about {int} seconds', async function (name, seconds) {
  const got = await this.admin('GET', `/sessions/${this.sessions[name].id}`);
  const left = (Date.parse(got.expiresAt) - Date.now()) / 1000;
  assert.ok(Math.abs(left - seconds) < 30, `expires in ${left}s`);
});

Then('the admin API lists {int} shadow mocks', async function (n) {
  const { sessions } = await this.admin('GET', '/sessions');
  assert.equal(sessions.length, n);
});

Then('the admin API still has the shadow mock {string}', async function (name) {
  const { sessions } = await this.admin('GET', '/sessions');
  assert.ok(sessions.some((s) => s.id === this.sessions[name].id));
});

Then('the admin API no longer has the shadow mock {string}', async function (name) {
  const { sessions } = await this.admin('GET', '/sessions');
  assert.ok(!sessions.some((s) => s.id === this.sessions[name].id));
});

// ---- Counters -----------------------------------------------------------------------------------------------

When('I choose the {string} scope', async function (label) {
  const select = this.page.locator('#counters-scope:visible, #recordings-scope:visible');
  await select.selectOption({ label });
});

Then('the counter for {string} shows {int} calls', async function (id, n) {
  const r = row(this, '#counters-body tr', id);
  await r.first().waitFor();
  await r.locator('td').nth(2).filter({ hasText: new RegExp(`^${n}$`) }).waitFor();
});

Then('the console says there are no call counters', async function () {
  await this.page.locator('#counters-empty').waitFor({ state: 'visible' });
});

// A reset counter is zero: the console shows either a row with 0 calls or no row at all.
Then('the counter for {string} is back to zero', async function (id) {
  const deadline = Date.now() + 10000;
  for (;;) {
    const r = row(this, '#counters-body tr', id);
    if ((await r.count()) === 0 || (await r.locator('td').nth(2).innerText()).trim() === '0') return;
    assert.ok(Date.now() < deadline, `the counter for ${id} was not reset`);
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
});

When('I set the counter for {string} to {int}', async function (id, n) {
  const r = row(this, '#counters-body tr', id);
  await r.getByLabel(`New count for ${id}`).fill(String(n));
  await r.getByRole('button', { name: 'Set', exact: true }).click();
  await r.locator('td').nth(2).filter({ hasText: new RegExp(`^${n}$`) }).waitFor();
});

When('I reset the counter for {string}', async function (id) {
  await row(this, '#counters-body tr', id).getByRole('button', { name: 'Reset', exact: true }).click();
});

When('I reset all counters', async function () {
  await this.page.locator('#reset-counters').click();
});

// ---- Recordings ---------------------------------------------------------------------------------------------

Then('the recording status is {string}', async function (status) {
  await this.page.locator('#recording-status', { hasText: new RegExp(`^${status}$`) }).waitFor();
});

When('I start recording through the upstream server', async function () {
  await this.page.locator('#rec-target').fill(this.upstream);
  await this.page.getByRole('button', { name: 'Start recording' }).click();
});

When('I stop recording', async function () {
  await this.page.getByRole('button', { name: 'Stop recording' }).click();
});

Then('the recording result shows {string}', async function (text) {
  await this.page.locator('#recording-result').getByText(text).first().waitFor();
});

// ---- Dry run ------------------------------------------------------------------------------------------------

When('I dry-run {string} {string}', async function (method, url) {
  await this.page.locator('#dr-method').fill(method);
  await this.page.locator('#dr-url').fill(url);
  await this.page.locator('#dr-session').fill('');
  await this.page.locator('#dryrun-form').getByRole('button', { name: 'Dry run' }).click();
});

When('I dry-run {string} {string} in the shadow mock {string}', async function (method, url, name) {
  await this.page.locator('#dr-method').fill(method);
  await this.page.locator('#dr-url').fill(url);
  await this.page.locator('#dr-session').fill(this.sessions[name].id);
  await this.page.locator('#dryrun-form').getByRole('button', { name: 'Dry run' }).click();
});

Then('the dry run shows {string}', async function (text) {
  await this.page.locator('#dryrun-result').getByText(text).first().waitFor();
});

Then('the journal still holds {int} requests', async function (n) {
  const got = await this.admin('GET', '/requests?limit=100');
  assert.equal((got.requests || []).length, n);
});

// ---- Signing in ---------------------------------------------------------------------------------------------

When('I enter the admin token', async function () {
  const field = this.page.locator('#token');
  await field.fill(this.adminToken);
  await field.dispatchEvent('change');
});

Then('the admin token field hides what is typed', async function () {
  assert.equal(await this.page.locator('#token').getAttribute('type'), 'password');
});
