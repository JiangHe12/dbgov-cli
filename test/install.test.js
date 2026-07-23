const assert = require('node:assert/strict');
const crypto = require('node:crypto');
const { EventEmitter } = require('node:events');
const fs = require('node:fs');
const http = require('node:http');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');

const packageJson = require('../package.json');
const { _test: installer } = require('../scripts/install.js');

const REPO = packageJson.repository.url
  .replace(/^git\+https:\/\/github\.com\//, '')
  .replace(/\.git$/, '');
const COMMAND = packageJson.name.replace(/-cli$/, '');
const platformName = process.platform === 'win32' ? 'windows' : process.platform;
const BINARY = installer.EXPECTED_BINARIES.find((name) => name.includes(`-${platformName}-`)) ||
  installer.EXPECTED_BINARIES[0];
const TEST_MAX_BYTES = 32;

function sha256(value) {
  return crypto.createHash('sha256').update(value).digest('hex');
}

function trustedPackage(content) {
  const binaries = Object.fromEntries(
    installer.EXPECTED_BINARIES.map((name) => [name, '0'.repeat(64)])
  );
  binaries[BINARY] = sha256(content);
  return {
    ...packageJson,
    binaryManifest: {
      schema: 1,
      repository: REPO,
      version: packageJson.version,
      binaries,
    },
  };
}

function clone(value) {
  return JSON.parse(JSON.stringify(value));
}

function listen(server) {
  return new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      server.removeListener('error', reject);
      resolve(server.address().port);
    });
  });
}

function close(server) {
  return new Promise((resolve, reject) => {
    server.close((err) => err ? reject(err) : resolve());
  });
}

function tempArtifacts(directory, destination) {
  const prefix = `.${path.basename(destination)}.`;
  return fs.readdirSync(directory).filter((name) =>
    name.startsWith(prefix) && name.endsWith('.tmp')
  );
}

test('package-bound manifest rejects tampered identity, digests, and entries', () => {
  assert.equal(installer.MAX_DOWNLOAD_BYTES, 256 * 1024 * 1024);
  assert.equal(installer.TOTAL_TIMEOUT_MS, 15 * 60 * 1000);

  const valid = trustedPackage('trusted-binary');
  assert.equal(installer.expectedDigest(valid, BINARY), sha256('trusted-binary'));

  const mutations = [
    (value) => { value.binaryManifest.version = '0.0.0'; },
    (value) => { value.binaryManifest.repository = 'attacker/repository'; },
    (value) => { value.repository.url = 'git+https://github.com/attacker/repository.git'; },
    (value) => { value.binaryManifest.binaries[BINARY] = 'z'.repeat(64); },
    (value) => { delete value.binaryManifest.binaries[BINARY]; },
    (value) => { value.binaryManifest.binaries[`${packageJson.name}-linux-riscv64`] = '1'.repeat(64); },
  ];
  for (const mutate of mutations) {
    const value = clone(valid);
    mutate(value);
    assert.throws(() => installer.expectedDigest(value, BINARY));
  }
});

test('verified install replaces only on success and always removes temporary files', async (t) => {
  const trusted = 'trusted-binary';
  const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), `${packageJson.name}-installer-`));
  const destination = path.join(tempDir, COMMAND);
  installer.ALLOWED_REDIRECT_HOSTS.add('127.0.0.1');
  const activeIntervals = new Set();

  const server = http.createServer((req, res) => {
    switch (req.url) {
      case '/trusted':
        res.end(trusted);
        break;
      case '/tampered':
        res.end('tampered-binary');
        break;
      case '/interrupted':
        res.writeHead(200, { 'Content-Length': '1000' });
        res.write('partial');
        res.socket.destroy();
        break;
      case '/redirect':
        res.writeHead(308, { Location: '/trusted' });
        res.end();
        break;
      case '/redirect-chain':
        res.writeHead(307, { Location: '/redirect' });
        res.end();
        break;
      case '/blocked':
        res.writeHead(302, { Location: 'https://example.invalid/binary' });
        res.end();
        break;
      case '/ftp-redirect':
        res.writeHead(302, { Location: 'ftp://127.0.0.1/binary' });
        res.end();
        break;
      case '/oversized-length':
        res.writeHead(200, { 'Content-Length': String(TEST_MAX_BYTES + 1) });
        res.end('x');
        break;
      case '/missing-length':
        res.writeHead(200);
        res.write(Buffer.alloc(TEST_MAX_BYTES));
        res.end(Buffer.alloc(1));
        break;
      case '/forged-length':
        res.writeHead(200, { 'Content-Length': '1' });
        res.end(Buffer.alloc(TEST_MAX_BYTES + 1));
        break;
      case '/slow': {
        res.writeHead(200);
        const interval = setInterval(() => res.write('x'), 5);
        activeIntervals.add(interval);
        res.on('close', () => {
          clearInterval(interval);
          activeIntervals.delete(interval);
        });
        break;
      }
      default:
        res.writeHead(404);
        res.end();
    }
  });
  const port = await listen(server);
  t.after(async () => {
    for (const interval of activeIntervals) clearInterval(interval);
    installer.ALLOWED_REDIRECT_HOSTS.delete('127.0.0.1');
    await close(server);
    fs.rmSync(tempDir, { recursive: true, force: true });
  });

  const install = (route, manifest = trustedPackage(trusted)) =>
    installer.installBinary(`http://127.0.0.1:${port}${route}`, destination, BINARY, manifest);
  const installURLWithPolicy = (url, policy, manifest = trustedPackage(trusted)) =>
    installer.installBinaryWithDownload(
      url,
      destination,
      BINARY,
      manifest,
      (downloadURL, temp) => installer.downloadWithLimits(downloadURL, temp, policy)
    );
  const installWithPolicy = (route, policy, manifest = trustedPackage(trusted)) =>
    installURLWithPolicy(`http://127.0.0.1:${port}${route}`, policy, manifest);

  fs.writeFileSync(destination, 'old-binary');
  await install('/trusted');
  assert.equal(fs.readFileSync(destination, 'utf8'), trusted);
  if (process.platform !== 'win32') {
    assert.equal(fs.statSync(destination).mode & 0o777, 0o755);
  }
  assert.deepEqual(tempArtifacts(tempDir, destination), []);

  for (const route of ['/tampered', '/interrupted', '/blocked', '/ftp-redirect']) {
    fs.writeFileSync(destination, 'old-binary');
    await assert.rejects(install(route));
    assert.equal(fs.readFileSync(destination, 'utf8'), 'old-binary');
    assert.deepEqual(tempArtifacts(tempDir, destination), []);
  }

  fs.writeFileSync(destination, 'old-binary');
  await install('/redirect');
  assert.equal(fs.readFileSync(destination, 'utf8'), trusted);
  assert.deepEqual(tempArtifacts(tempDir, destination), []);

  const constrainedPolicy = {
    idleTimeoutMs: 500,
    maxBytes: TEST_MAX_BYTES,
    totalTimeoutMs: 1000,
  };
  for (const route of ['/oversized-length', '/missing-length', '/forged-length']) {
    fs.writeFileSync(destination, 'old-binary');
    await assert.rejects(installWithPolicy(route, constrainedPolicy));
    assert.equal(fs.readFileSync(destination, 'utf8'), 'old-binary');
    assert.deepEqual(tempArtifacts(tempDir, destination), []);
  }

  const timerToken = {};
  let totalTimerCleared = false;
  fs.writeFileSync(destination, 'old-binary');
  await assert.rejects(
    installURLWithPolicy('not a valid URL', {
      ...constrainedPolicy,
      clearTimer(token) {
        assert.equal(token, timerToken);
        totalTimerCleared = true;
      },
      setTimer() {
        return timerToken;
      },
    })
  );
  assert.equal(totalTimerCleared, true);
  assert.equal(fs.readFileSync(destination, 'utf8'), 'old-binary');
  assert.deepEqual(tempArtifacts(tempDir, destination), []);

  for (let attempt = 0; attempt < 25; attempt += 1) {
    fs.writeFileSync(destination, 'old-binary');
    await assert.rejects(
      installWithPolicy('/slow', {
        ...constrainedPolicy,
        maxBytes: 1024,
        totalTimeoutMs: 10,
      }),
      /total deadline/
    );
    assert.equal(fs.readFileSync(destination, 'utf8'), 'old-binary');
    assert.deepEqual(tempArtifacts(tempDir, destination), []);
  }

  let totalTimerCount = 0;
  fs.writeFileSync(destination, 'old-binary');
  await installWithPolicy('/redirect-chain', {
    ...constrainedPolicy,
    setTimer(callback, milliseconds) {
      totalTimerCount += 1;
      return setTimeout(callback, milliseconds);
    },
  });
  assert.equal(totalTimerCount, 1);
  assert.equal(fs.readFileSync(destination, 'utf8'), trusted);
  assert.deepEqual(tempArtifacts(tempDir, destination), []);
});

test('idle timeout destroys the underlying request and response', () => {
  const originalGet = http.get;
  let timeoutCallback;
  let destroyedWith;
  let responseDestroyedWith;
  http.get = (_url, onResponse) => {
    const request = new EventEmitter();
    request.setTimeout = (milliseconds, callback) => {
      assert.equal(milliseconds, 30000);
      timeoutCallback = callback;
    };
    request.destroy = (err) => { destroyedWith = err; };
    onResponse({
      destroyed: false,
      destroy(err) {
        this.destroyed = true;
        responseDestroyedWith = err;
      },
    });
    return request;
  };
  try {
    installer.request('http://example.com/download', () => {});
    timeoutCallback();
  } finally {
    http.get = originalGet;
  }
  assert.match(destroyedWith.message, /idle timeout after 30s/);
  assert.equal(responseDestroyedWith, destroyedWith);
});

test('release workflow verifies signed assets and embeds the provenance-bound manifest', () => {
  const installerSource = fs.readFileSync(path.join(__dirname, '..', 'scripts', 'install.js'), 'utf8');
  const workflow = fs.readFileSync(path.join(__dirname, '..', '.github', 'workflows', 'release.yml'), 'utf8');

  assert.doesNotMatch(installerSource, /SKIP_VERIFY|checksums\.txt|downloadToString|parseChecksums/);
  assert.equal((workflow.match(/cosign-release: 'v2\.6\.4'/g) || []).length, 4);
  assert.ok((workflow.match(/cosign verify-blob/g) || []).length >= 4);
  assert.match(workflow, /--certificate-oidc-issuer "\$\{OIDC_ISSUER\}"/);
  assert.match(workflow, /https:\/\/token\.actions\.githubusercontent\.com/);
  assert.match(workflow, /https:\/\/github\.com\/\$\{GITHUB_REPOSITORY\}\/\.github\/workflows\/release\.yml@\$\{GITHUB_REF\}/);
  assert.equal((workflow.match(/sha256sum --check --strict checksums\.txt/g) || []).length, 2);
  assert.equal((workflow.match(/test "\$\(wc -l < checksums\.txt\)" -eq 6/g) || []).length, 3);
  assert.match(workflow, /binaryManifest/);
  assert.match(workflow, /npm publish --provenance --access public/);
  for (const binary of installer.EXPECTED_BINARIES) {
    assert.match(workflow, new RegExp(binary.replace('.', '\\.')));
  }
});
