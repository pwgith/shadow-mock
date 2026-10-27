// Client compatibility suite.
//
// Talks to a running server the way a WireMock user's tests do: over HTTP, with no knowledge of how
// it is built. It must pass against a real WireMock and against Shadow Mock, so every expectation
// here has been confirmed by WireMock itself.
//
//   BASE_URL=http://localhost:8080 node --test test/client/compat.test.mjs
//
// CI runs it against a WireMock container and against the built Shadow Mock binary.

import assert from 'node:assert/strict';
import { beforeEach, describe, it } from 'node:test';

const base = (process.env.BASE_URL ?? 'http://localhost:8080').replace(/\/$/, '');

async function call(method, path, body, headers = {}) {
  const init = { method, headers: { ...headers } };
  if (body !== undefined) {
    init.body = typeof body === 'string' ? body : JSON.stringify(body);
    init.headers['Content-Type'] ??= 'application/json';
  }
  const res = await fetch(base + path, init);
  const text = await res.text();
  let json;
  try {
    json = JSON.parse(text);
  } catch {
    json = undefined;
  }
  return { status: res.status, text, json, headers: res.headers };
}

const admin = (method, path, body) => call(method, `/__admin${path}`, body);

async function stub(mapping) {
  const res = await admin('POST', '/mappings', mapping);
  assert.equal(res.status, 201, `creating a stub: ${res.text}`);
  return res.json;
}

async function journal() {
  const res = await admin('GET', '/requests');
  assert.equal(res.status, 200);
  return res.json.requests; // most recent first
}

describe('stubs and the journal', () => {
  beforeEach(async () => {
    const res = await admin('POST', '/reset');
    assert.equal(res.status, 200);
  });

  it('serves a stub created through the admin API', async () => {
    await stub({ request: { method: 'GET', url: '/hello' }, response: { status: 200, body: 'Hi' } });
    const res = await call('GET', '/hello');
    assert.equal(res.status, 200);
    assert.equal(res.text, 'Hi');
  });

  it('returns the stub headers and status', async () => {
    await stub({
      request: { method: 'GET', url: '/teapot' },
      response: { status: 418, headers: { 'Content-Type': 'text/plain', 'X-Custom': 'yes' }, body: 'short and stout' },
    });
    const res = await call('GET', '/teapot');
    assert.equal(res.status, 418);
    assert.equal(res.headers.get('x-custom'), 'yes');
    assert.match(res.headers.get('content-type'), /^text\/plain/);
  });

  it('records each request in the journal, most recent first', async () => {
    await stub({ request: { method: 'GET', urlPathPattern: '/n/.*' }, response: { status: 200 } });
    await call('GET', '/n/1');
    await call('GET', '/n/2');
    await call('GET', '/n/3');
    const entries = await journal();
    assert.deepEqual(entries.map((e) => e.request.url), ['/n/3', '/n/2', '/n/1']);
    assert.ok(entries.every((e) => e.wasMatched === true));
  });

  it('keeps a large response whole in the journal', async () => {
    const body = 'x'.repeat(200_000) + 'END';
    await stub({ request: { method: 'GET', url: '/big' }, response: { status: 200, body } });
    await call('GET', '/big');
    const [entry] = await journal();
    assert.equal(entry.response.body.length, body.length);
    assert.ok(entry.response.body.endsWith('END'));
  });

  it('keeps a large request body whole in the journal', async () => {
    await stub({ request: { method: 'POST', url: '/upload' }, response: { status: 201 } });
    const body = JSON.stringify({ items: Array.from({ length: 4000 }, (_, i) => ({ id: i, ref: `UPLOAD-${i}` })) });
    assert.ok(body.length > 100_000);
    const res = await call('POST', '/upload', body);
    assert.equal(res.status, 201);
    const [entry] = await journal();
    assert.equal(entry.request.body.length, body.length);
    assert.deepEqual(JSON.parse(entry.request.body).items.length, 4000);
  });

  it('counts and finds requests by pattern', async () => {
    await stub({ request: { method: 'POST', url: '/orders' }, response: { status: 201 } });
    await call('POST', '/orders', { id: 1, kind: 'a' });
    await call('POST', '/orders', { id: 2, kind: 'b' });
    const count = await admin('POST', '/requests/count', { method: 'POST', url: '/orders' });
    assert.equal(count.json.count, 2);
    const found = await admin('POST', '/requests/find', {
      method: 'POST',
      url: '/orders',
      bodyPatterns: [{ matchesJsonPath: { expression: '$.kind', equalTo: 'b' } }],
    });
    assert.equal(found.json.requests.length, 1);
    assert.equal(JSON.parse(found.json.requests[0].body).id, 2);
  });

  it('empties the journal and the stubs on reset', async () => {
    await stub({ request: { method: 'GET', url: '/gone' }, response: { status: 200 } });
    await call('GET', '/gone');
    await admin('POST', '/reset');
    assert.deepEqual(await journal(), []);
    const list = await admin('GET', '/mappings');
    assert.equal(list.json.mappings.length, 0);
  });

  it('imports several stubs at once and deletes one by id', async () => {
    await admin('POST', '/mappings/import', {
      mappings: [
        { id: 'a0000000-0000-4000-8000-000000000001', request: { url: '/one' }, response: { status: 200, body: '1' } },
        { id: 'a0000000-0000-4000-8000-000000000002', request: { url: '/two' }, response: { status: 200, body: '2' } },
      ],
    });
    assert.equal((await call('GET', '/two')).text, '2');
    const del = await admin('DELETE', '/mappings/a0000000-0000-4000-8000-000000000002');
    assert.equal(del.status, 200);
    assert.equal((await call('GET', '/two')).status, 404);
    assert.equal((await call('GET', '/one')).text, '1');
  });
});

describe('unmatched requests', () => {
  beforeEach(() => admin('POST', '/reset'));

  it('answers 404 and explains the nearest stub', async () => {
    await stub({ request: { method: 'GET', url: '/hello' }, response: { status: 200 } });
    const res = await call('GET', '/hellp');
    assert.equal(res.status, 404);
    assert.match(res.text, /Request was not matched/);
  });

  it('lists unmatched requests and their near misses', async () => {
    await stub({ request: { method: 'GET', url: '/hello' }, response: { status: 200 } });
    await call('GET', '/hellp');
    const unmatched = await admin('GET', '/requests/unmatched');
    assert.equal(unmatched.json.requests.length, 1);
    assert.equal(unmatched.json.requests[0].url, '/hellp');
    const near = await admin('POST', '/near-misses/request', { method: 'GET', url: '/hellp' });
    assert.equal(near.json.nearMisses.length, 1);
    assert.equal(near.json.nearMisses[0].stubMapping.request.url, '/hello');
  });
});

describe('request matching', () => {
  beforeEach(() => admin('POST', '/reset'));

  it('matches an equalToXml body ignoring layout', async () => {
    await stub({
      request: { method: 'POST', url: '/xml', bodyPatterns: [{ equalToXml: '<a><b>1</b></a>' }] },
      response: { status: 200, body: 'matched' },
    });
    assert.equal((await call('POST', '/xml', '<a>\n  <b>1</b>\n</a>', { 'Content-Type': 'application/xml' })).text, 'matched');
    assert.equal((await call('POST', '/xml', '<a><b>2</b></a>', { 'Content-Type': 'application/xml' })).status, 404);
  });

  it('prefers the lower priority number', async () => {
    await stub({ priority: 5, request: { url: '/p' }, response: { status: 200, body: 'five' } });
    await stub({ priority: 1, request: { url: '/p' }, response: { status: 200, body: 'one' } });
    assert.equal((await call('GET', '/p')).text, 'one');
  });

  it('matches headers and query parameters', async () => {
    await stub({
      request: {
        method: 'GET',
        urlPath: '/h',
        headers: { 'X-Tenant': { equalTo: 'acme' } },
        queryParameters: { page: { matches: '[0-9]+' } },
      },
      response: { status: 200, body: 'ok' },
    });
    assert.equal((await call('GET', '/h?page=3', undefined, { 'X-Tenant': 'acme' })).text, 'ok');
    assert.equal((await call('GET', '/h?page=x', undefined, { 'X-Tenant': 'acme' })).status, 404);
    assert.equal((await call('GET', '/h?page=3')).status, 404);
  });
});

describe('responses', () => {
  beforeEach(() => admin('POST', '/reset'));

  it('waits for a fixed delay', async () => {
    await stub({ request: { url: '/slow' }, response: { status: 200, fixedDelayMilliseconds: 300 } });
    const started = Date.now();
    await call('GET', '/slow');
    assert.ok(Date.now() - started >= 290, 'the response arrived too early');
  });

  it('renders response templates', async () => {
    await stub({
      request: { method: 'POST', urlPath: '/t' },
      response: {
        status: 200,
        transformers: ['response-template'],
        body: "{{request.method}} {{request.path}} {{jsonPath request.body '$.name'}} {{request.query.q}}",
      },
    });
    const res = await call('POST', '/t?q=zed', { name: 'Ada' });
    assert.equal(res.text, 'POST /t Ada zed');
  });

  it('renders inline partials and block parameters', async () => {
    await stub({
      request: { method: 'POST', url: '/partials' },
      response: {
        status: 200,
        transformers: ['response-template'],
        body:
          '{{#*inline "row"}}[{{this}}]{{/inline}}' +
          '{{#with (parseJson request.body) as |req|}}{{#each req.rows as |r|}}{{> row r}}{{#unless @last}},{{/unless}}{{/each}}{{/with}}',
      },
    });
    const res = await call('POST', '/partials', { rows: ['a', 'b', 'c'] });
    assert.equal(res.text, '[a],[b],[c]');
  });

  it('writes JSON values without escaping markup', async () => {
    await stub({
      request: { method: 'POST', url: '/json' },
      response: {
        status: 200,
        transformers: ['response-template'],
        // A space before the closing brace: WireMock reads {{x}}} as a syntax error.
        body: '{{#with (parseJson request.body) as |req|}}{"name": {{toJson req.name}} }{{/with}}',
      },
    });
    const res = await call('POST', '/json', { name: 'A & <B>' });
    assert.deepEqual(res.json, { name: 'A & <B>' });
  });
});

describe('template helpers', () => {
  beforeEach(() => admin('POST', '/reset'));

  const render = async (body, { method = 'POST', path = '/h', payload = '', type = 'text/plain' } = {}) => {
    await stub({
      request: { method, urlPath: '/h' },
      response: { status: 200, transformers: ['response-template'], body },
    });
    return call(method, path, payload === '' ? undefined : payload, { 'Content-Type': type });
  };

  it('formats and parses dates', async () => {
    const res = await render("{{date (parseDate request.body format='dd/MM/yyyy') format='yyyy-MM-dd'}}", { payload: '31/12/2025' });
    assert.equal(res.text, '2025-12-31');
  });

  it('reads a url-encoded form', async () => {
    const res = await render("{{formData request.body 'form' urlDecode=true}}Hello {{form.firstName}} {{form.lastName}}", {
      payload: 'firstName=Ada%20L&lastName=Lovelace',
      type: 'application/x-www-form-urlencoded',
    });
    assert.equal(res.text, 'Hello Ada L Lovelace');
  });

  it('evaluates an XPath inside a SOAP body', async () => {
    const envelope =
      '<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><k:GetClaim xmlns:k="urn:c"><k:ClaimId>42</k:ClaimId></k:GetClaim></s:Body></s:Envelope>';
    const res = await render("{{soapXPath request.body '/GetClaim/ClaimId/text()'}}", { payload: envelope, type: 'text/xml' });
    assert.equal(res.text, '42');
  });

  it('picks from the values it is given', async () => {
    assert.equal((await render("{{pickRandom 'only'}}")).text, 'only');
    assert.equal((await render("{{pickRandom (array 'solo')}}")).text, 'solo');
  });

  it('gives a random decimal in range', async () => {
    const value = Number((await render('{{randomDecimal lower=1 upper=2}}')).text);
    assert.ok(value >= 1 && value <= 2, `got ${value}`);
  });

  it('matches text against a regular expression, as a block or an expression', async () => {
    const res = await render(
      "{{#matches request.body '[0-9]+'}}numeric{{else}}no{{/matches}} {{#if (matches request.body 'abc')}}abc{{else}}not abc{{/if}}",
      { payload: '123' },
    );
    assert.equal(res.text, 'numeric not abc');
  });

  it('uses a fallback for a missing value', async () => {
    const res = await render("[{{val request.query.missing or='d'}}][{{val request.query.here or='d'}}]", {
      path: '/h?here=1',
      method: 'GET',
    });
    assert.equal(res.text, '[d][1]');
  });

  it('adds to and joins lists, and counts a range', async () => {
    const res = await render("{{arrayJoin ', ' (arrayAdd (array 'b' 'c') 'a' position='start')}} {{#each (range 1 3)}}[{{this}}]{{/each}}", {
      method: 'GET',
    });
    assert.equal(res.text, 'a, b, c [1][2][3]');
  });

  it('reaches a single query value as .first', async () => {
    const res = await render('{{request.query.word.first}}|{{request.query.word.[0]}}', { path: '/h?word=abc', method: 'GET' });
    assert.equal(res.text, 'abc|abc');
  });

  it('includes the Host header among the request headers', async () => {
    const res = await render('{{toJson request.headers}}', { method: 'GET' });
    assert.match(JSON.parse(res.text).Host, /^localhost:\d+$/);
  });

  it('fails a response that calls a helper that does not exist', async () => {
    const res = await render("{{nosuchhelper 'x' 'y'}}", { method: 'GET' });
    assert.equal(res.status, 500);
  });

  it('sends no Content-Type when the stub gives none', async () => {
    await stub({ request: { url: '/bare' }, response: { status: 200, jsonBody: { a: 1 } } });
    const res = await call('GET', '/bare');
    assert.equal(res.headers.get('content-type'), null);
  });

  it('records the Host header, the base64 body and the form fields in the journal', async () => {
    await stub({ request: { method: 'POST', url: '/f' }, response: { status: 201 } });
    await call('POST', '/f', 'k=v&k=w', { 'Content-Type': 'application/x-www-form-urlencoded' });
    const [entry] = await journal();
    assert.equal(entry.request.bodyAsBase64, Buffer.from('k=v&k=w').toString('base64'));
    assert.equal(entry.request.protocol, 'HTTP/1.1');
    assert.equal(entry.request.scheme, 'http');
    assert.deepEqual(entry.request.formParams.k.values, ['v', 'w']);
    assert.match(entry.request.headers.Host, /^localhost:\d+$/);
  });
});
