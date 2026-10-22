import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import { checkForUpdates, selectRelease, releasesURL } from './client.js';

const fixtures = JSON.parse(readFileSync(new URL('fixtures.json', import.meta.url)));
for (const c of fixtures) test(c.name, () => {
  const got = selectRelease(c.tool, c.current, c.releases);
  assert.equal(got.status, c.status);
  assert.equal(got.latest, c.latest);
  assert.ok(got.release_url.startsWith('https://github.com/zerotrustgarage/tools/releases'));
});

test('bounded HTTPS requests use no credentials or referrer and paginate mixed products', async () => {
  const requests = [];
  const fixture = fixtures.find(f => f.name === 'scanner update');
  const result = await checkForUpdates('scanner', '0.1.0', { fetcher: async (url, options) => {
    requests.push(url);
    assert.ok(url.startsWith('https://api.github.com/repos/zerotrustgarage/tools/releases?'));
    assert.equal(options.credentials, 'omit'); assert.equal(options.redirect, 'error');
    assert.equal(options.referrerPolicy, 'no-referrer');
    return new Response(JSON.stringify(requests.length === 1 ? Array(100).fill({}) : fixture.releases));
  } });
  assert.equal(requests.length, 2); assert.equal(result.latest, '0.5.0');
});

test('failure never reports that an application is current', async () => {
  for (const [response, message] of [
    [new Response('{}', { status: 403 }), /rate limit/],
    [new Response('{}', { status: 503 }), /HTTP 503/],
    [new Response('null'), /Invalid update response/],
    [new Response('{'), /Invalid update response/],
    [new Response(' '.repeat(4 * 1024 * 1024 + 1)), /too large/],
  ]) await assert.rejects(checkForUpdates('scanner', '1.0.0', { fetcher: async () => response }), message);
  await assert.rejects(checkForUpdates('readout', '1.0.0', { fetcher: async () => { throw new TypeError('offline'); } }), /connection/);
});

test('pagination exhaustion is an error; local inputs are validated before a request', async () => {
  let requests = 0;
  await assert.rejects(checkForUpdates('scanner', '1.0.0', { fetcher: async () => {
    requests++; return new Response(JSON.stringify(Array(100).fill({})));
  } }), /history limit/);
  assert.equal(requests, 10);
  for (const [tool, version] of [['not-a-tool', '1.0.0'], ['scanner', 'bad\nversion']])
    await assert.rejects(checkForUpdates(tool, version, { fetcher: () => { throw new Error('must not fetch'); } }));
  assert.throws(() => releasesURL('__proto__'));
});
