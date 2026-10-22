#!/usr/bin/env node
// Creates a GitHub release and uploads its assets through the REST API. The organization's
// self-hosted runners may not have the gh CLI, so this uses Node's fetch and GITHUB_TOKEN.
//   node scripts/ci/github-release.mjs <tag> <title> <notes> <file>...
import { readFile, stat } from 'node:fs/promises';
import { basename } from 'node:path';

const [tag, title, notes, ...files] = process.argv.slice(2);
const { GITHUB_TOKEN: token, GITHUB_REPOSITORY: repo, GITHUB_SHA: sha } = process.env;
if (!tag || !title || !files.length || !token || !repo || !sha) {
  console.error('Usage: github-release.mjs <tag> <title> <notes> <file>... (needs GITHUB_TOKEN, GITHUB_REPOSITORY, GITHUB_SHA)');
  process.exit(2);
}
const api = `https://api.github.com/repos/${repo}`;
const headers = { authorization: `Bearer ${token}`, accept: 'application/vnd.github+json', 'x-github-api-version': '2022-11-28' };

async function call(url, init) {
  const response = await fetch(url, { ...init, headers: { ...headers, ...init.headers }, redirect: 'error' });
  if (!response.ok) throw new Error(`${init.method} ${new URL(url).pathname} failed: HTTP ${response.status} ${await response.text()}`);
  return response.json();
}

const release = await call(`${api}/releases`, {
  method: 'POST',
  headers: { 'content-type': 'application/json' },
  body: JSON.stringify({ tag_name: tag, target_commitish: sha, name: title, body: notes, draft: true }),
});
try {
  for (const file of files) {
    const name = basename(file);
    const upload = new URL(release.upload_url.replace(/\{.*\}$/, ''));
    upload.searchParams.set('name', name);
    const type = name.endsWith('.exe') ? 'application/vnd.microsoft.portable-executable' : 'text/plain';
    await call(upload, {
      method: 'POST',
      headers: { 'content-type': type, 'content-length': String((await stat(file)).size) },
      body: await readFile(file),
    });
    console.log(`Uploaded ${name}`);
  }
  // Published only once every asset is in place, so nobody downloads a partial release.
  await call(`${api}/releases/${release.id}`, { method: 'PATCH', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ draft: false }) });
  console.log(`Published ${release.html_url}`);
} catch (error) {
  await fetch(`${api}/releases/${release.id}`, { method: 'DELETE', headers }).catch(() => undefined);
  throw error;
}
