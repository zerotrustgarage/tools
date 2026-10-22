// Throwaway certificate authority for tests: a code signing leaf whose subject plays the
// pinned publisher, a second customer's leaf from the same CA, and a TLS-only leaf. Nothing
// here is a Microsoft root or a real publisher.
import { execFileSync } from 'node:child_process';
import { mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const openssl = (args) => execFileSync('openssl', args, { stdio: ['ignore', 'pipe', 'pipe'] });

export function hasTool(name) {
  try {
    execFileSync(name, ['--version'], { stdio: 'ignore' });
    return true;
  } catch (error) {
    // osslsigncode and makensis return non-zero for --version on some builds but exist.
    return error?.code !== 'ENOENT';
  }
}

export function createTestCa() {
  const dir = mkdtempSync(join(tmpdir(), 'ztg-test-ca-'));
  const f = (name) => join(dir, name);
  openssl(['req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', f('ca.key'), '-out', f('ca.pem'), '-days', '2',
    '-subj', '/CN=ZTG Test Root', '-addext', 'basicConstraints=critical,CA:TRUE', '-addext', 'keyUsage=critical,keyCertSign']);
  const leaf = (name, subject, eku) => {
    writeFileSync(f(`${name}.ext`), `extendedKeyUsage=${eku}\nkeyUsage=critical,digitalSignature\nbasicConstraints=CA:FALSE\n`);
    openssl(['req', '-newkey', 'rsa:2048', '-nodes', '-keyout', f(`${name}.key`), '-out', f(`${name}.csr`), '-subj', subject]);
    openssl(['x509', '-req', '-in', f(`${name}.csr`), '-CA', f('ca.pem'), '-CAkey', f('ca.key'), '-CAcreateserial',
      '-out', f(`${name}.pem`), '-days', '2', '-extfile', f(`${name}.ext`)]);
    return { cert: f(`${name}.pem`), key: f(`${name}.key`) };
  };
  return {
    dir,
    ca: f('ca.pem'),
    publisher: leaf('publisher', '/C=US/ST=North Carolina/O=Test Publisher LLC/CN=Test Publisher LLC', 'codeSigning'),
    otherCustomer: leaf('other', '/C=US/O=Someone Else/CN=Someone Else', 'codeSigning'),
    tlsOnly: leaf('tls', '/C=US/ST=North Carolina/O=Test Publisher LLC/CN=Test Publisher LLC', 'serverAuth'),
    publisherSubject: 'CN=Test Publisher LLC,O=Test Publisher LLC,ST=North Carolina,C=US',
  };
}
