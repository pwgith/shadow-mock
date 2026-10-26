#!/usr/bin/env node
// Validates the mapping corpus in corpus/: every JSON file parses, mapping ids are
// unique UUIDs, mappings have a request and a response (or callResponses-only rules),
// nothing under mappings/ uses WireMock scenario fields (FR-4.4), call rules are well
// formed (FR-4.1), every bodyFileName exists, and every negative file declares _expect.
//
// Usage: node scripts/check-corpus.js [corpusDir]
'use strict';

const fs = require('fs');
const path = require('path');

const root = path.resolve(process.argv[2] || path.join(__dirname, '..', 'corpus'));
const problems = [];
const ids = new Map();
let mappingCount = 0;
let fileCount = 0;

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const CALLS = /^([1-9][0-9]*)(-([1-9][0-9]*)|\+)?$/;
const SCENARIO_FIELDS = ['scenarioName', 'requiredScenarioState', 'newScenarioState'];

function walk(dir) {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = path.join(dir, e.name);
    return e.isDirectory() ? walk(p) : [p];
  });
}

const rel = (p) => path.relative(root, p).split(path.sep).join('/');
const fail = (file, msg) => problems.push(`${rel(file)}: ${msg}`);

function treeOf(file) {
  const r = rel(file);
  return r.startsWith('shadow-mock/') ? 'shadow-mock' : 'wiremock';
}

function checkCallRules(file, m) {
  if (m.callResponses === undefined) return;
  if (!Array.isArray(m.callResponses)) return fail(file, `${m.id}: callResponses must be an array`);
  m.callResponses.forEach((r, i) => {
    const where = `${m.id} callResponses[${i}]`;
    const hasCalls = r.calls !== undefined;
    const hasEvery = r.every !== undefined;
    if (hasCalls === hasEvery) fail(file, `${where}: exactly one of calls or every is required`);
    if (hasCalls) {
      const mm = CALLS.exec(String(r.calls));
      if (!mm) fail(file, `${where}: invalid calls "${r.calls}"`);
      else if (mm[3] && Number(mm[3]) < Number(mm[1])) fail(file, `${where}: range runs backwards`);
    }
    if (hasEvery && !(Number.isInteger(r.every) && r.every >= 2)) fail(file, `${where}: every must be an integer >= 2`);
    if (!r.response || typeof r.response !== 'object') fail(file, `${where}: response is required`);
  });
}

function bodyFiles(m) {
  const out = [];
  const add = (resp) => resp && resp.bodyFileName && !resp.bodyFileName.includes('{{') && out.push(resp.bodyFileName);
  add(m.response);
  (m.callResponses || []).forEach((r) => add(r.response));
  return out;
}

function checkMapping(file, m, opts) {
  mappingCount++;
  if (!m.request || typeof m.request !== 'object') fail(file, `${m.id || '(no id)'}: request is required`);
  if (!m.response || typeof m.response !== 'object') fail(file, `${m.id || '(no id)'}: response is required`);
  if (m.id !== undefined) {
    if (!UUID.test(m.id)) fail(file, `id "${m.id}" is not a UUID`);
    if (opts.unique) {
      if (ids.has(m.id) && !opts.overrideAllowed) fail(file, `duplicate id ${m.id} (also in ${ids.get(m.id)})`);
      if (!ids.has(m.id)) ids.set(m.id, rel(file));
    }
  }
  for (const f of SCENARIO_FIELDS) if (f in m) fail(file, `${m.id}: scenario field "${f}" is not supported (FR-4.4)`);
  if (opts.tree === 'wiremock' && (m.callResponses || (m.response && 'gzipDisabled' in m.response))) {
    fail(file, `${m.id}: Shadow Mock extensions do not belong in the wiremock tree`);
  }
  checkCallRules(file, m);
  for (const bf of bodyFiles(m)) {
    const candidates = [path.join(root, opts.tree, '__files', bf), path.join(root, 'shadow-mock', 'sessions', 'files', bf)];
    if (!candidates.some((c) => fs.existsSync(c)) && !m.name?.includes('does not exist')) fail(file, `${m.id}: bodyFileName "${bf}" not found`);
  }
}

for (const file of walk(root)) {
  const r = rel(file);
  if (!r.endsWith('.json')) continue;
  if (r.includes('/__files/templates/')) continue; // Handlebars templates, not JSON until rendered
  fileCount++;
  let doc;
  try {
    doc = JSON.parse(fs.readFileSync(file, 'utf8'));
  } catch (e) {
    fail(file, `invalid JSON: ${e.message}`);
    continue;
  }
  const tree = treeOf(file);
  const isNegative = r.includes('/negative/');
  if (isNegative) {
    if (!doc._expect) fail(file, 'negative files must declare _expect');
    continue;
  }
  const inMappings = /^(wiremock|shadow-mock)\/(mappings|extensions|mapping-sets)\//.test(r);
  const isSessionPayload = r.startsWith('shadow-mock/sessions/');
  if (inMappings) {
    const list = Array.isArray(doc.mappings) ? doc.mappings : [doc];
    list.forEach((m) => checkMapping(file, m, { tree, unique: true }));
  } else if (isSessionPayload && Array.isArray(doc.mappings)) {
    // Session mappings may reuse a global id on purpose (override by id, FR-7.7).
    doc.mappings.forEach((m) => checkMapping(file, m, { tree, unique: true, overrideAllowed: true }));
  } else if (isSessionPayload && doc.request && doc.response) {
    checkMapping(file, doc, { tree, unique: true, overrideAllowed: true });
  } else if (r.startsWith('wiremock/admin/mappings/import.json') && Array.isArray(doc.mappings)) {
    doc.mappings.forEach((m) => checkMapping(file, m, { tree, unique: true }));
  }
}

console.log(`corpus: ${fileCount} JSON files, ${mappingCount} mappings, ${ids.size} unique ids`);
if (problems.length) {
  problems.forEach((p) => console.log('PROBLEM: ' + p));
  console.log(`${problems.length} problem(s)`);
  process.exit(1);
}
console.log('OK');
