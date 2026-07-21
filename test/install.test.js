const assert = require('node:assert/strict');
const { EventEmitter } = require('node:events');
const fs = require('node:fs');
const http = require('node:http');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');

const { _test: installer } = require('../scripts/install.js');

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

test('binary download follows relative 301/302/307/308 redirects before creating the destination', async (t) => {
  const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'dbgov-installer-'));
  const dest = path.join(tempDir, 'dbgov');
  let statusCode = 301;
  installer.ALLOWED_REDIRECT_HOSTS.add('127.0.0.1');

  const server = http.createServer((req, res) => {
    if (req.url === '/asset') {
      assert.equal(fs.existsSync(dest), false);
      res.end(`binary-${statusCode}`);
      return;
    }
    res.writeHead(statusCode, { Location: '/asset' });
    res.end('redirect response body');
  });
  const port = await listen(server);

  t.after(async () => {
    installer.ALLOWED_REDIRECT_HOSTS.delete('127.0.0.1');
    await close(server);
    fs.rmSync(tempDir, { recursive: true, force: true });
  });

  for (statusCode of [301, 302, 307, 308]) {
    await installer.download(`http://127.0.0.1:${port}/start`, dest);
    assert.equal(fs.readFileSync(dest, 'utf8'), `binary-${statusCode}`);
    fs.unlinkSync(dest);
  }
});

test('binary download drains and rejects a redirect outside the allowlist without creating the destination', async (t) => {
  const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'dbgov-installer-'));
  const dest = path.join(tempDir, 'dbgov');
  const originalGet = http.get;
  const request = new EventEmitter();
  const response = {
    statusCode: 302,
    headers: { location: 'https://example.invalid/binary' },
    resumeCalled: false,
    resume() { this.resumeCalled = true; },
  };
  request.setTimeout = () => {};
  request.destroy = () => {};
  http.get = (url, onResponse) => {
    process.nextTick(() => onResponse(response));
    return request;
  };

  t.after(async () => {
    http.get = originalGet;
    fs.rmSync(tempDir, { recursive: true, force: true });
  });

  await assert.rejects(
    installer.download('http://example.com/start', dest),
    /Redirect to non-allowed host rejected/
  );
  assert.equal(fs.existsSync(dest), false);
  assert.equal(response.resumeCalled, true);
});

test('checksum download follows an allowed relative redirect and rejects a disallowed redirect', async (t) => {
  installer.ALLOWED_REDIRECT_HOSTS.add('127.0.0.1');
  const server = http.createServer((req, res) => {
    if (req.url === '/checksums.txt') {
      res.end('checksum-data');
      return;
    }
    if (req.url === '/allowed') {
      res.writeHead(308, { Location: '/checksums.txt' });
      res.end('redirect response body');
      return;
    }
    res.writeHead(307, { Location: 'https://example.invalid/checksums.txt' });
    res.end('redirect response body');
  });
  const port = await listen(server);

  t.after(async () => {
    installer.ALLOWED_REDIRECT_HOSTS.delete('127.0.0.1');
    await close(server);
  });

  assert.equal(
    await installer.downloadToString(`http://127.0.0.1:${port}/allowed`),
    'checksum-data'
  );
  await assert.rejects(
    installer.downloadToString(`http://127.0.0.1:${port}/blocked`),
    /Redirect to non-allowed host rejected/
  );
});

test('request timeout destroys the underlying request', () => {
  const originalGet = http.get;
  let timeoutCallback;
  let destroyedWith;

  http.get = () => ({
    setTimeout(milliseconds, callback) {
      assert.equal(milliseconds, 30000);
      timeoutCallback = callback;
    },
    destroy(err) {
      destroyedWith = err;
    },
  });

  try {
    installer.request('http://example.com/download', () => {});
    timeoutCallback();
  } finally {
    http.get = originalGet;
  }

  assert.match(destroyedWith.message, /timed out after 30s/);
});
