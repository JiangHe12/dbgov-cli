#!/usr/bin/env node

const crypto = require('crypto');
const fs = require('fs');
const http = require('http');
const https = require('https');
const path = require('path');
const { pipeline, Transform } = require('stream');
const { URL } = require('url');

const pkg = require('../package.json');
const VERSION = pkg.version;
const PACKAGE_NAME = 'dbgov-cli';
const REPO = 'JiangHe12/dbgov-cli';
const REPOSITORY_URL = `git+https://github.com/${REPO}.git`;
const IDLE_TIMEOUT_MS = 30000;
const TOTAL_TIMEOUT_MS = 15 * 60 * 1000;
const MAX_DOWNLOAD_BYTES = 256 * 1024 * 1024;
const MAX_REDIRECTS = 5;

const EXPECTED_BINARIES = Object.freeze([
  'dbgov-cli-darwin-amd64',
  'dbgov-cli-darwin-arm64',
  'dbgov-cli-linux-amd64',
  'dbgov-cli-linux-arm64',
  'dbgov-cli-windows-amd64.exe',
  'dbgov-cli-windows-arm64.exe',
]);

const ALLOWED_REDIRECT_HOSTS = new Set([
  'github.com',
  'objects.githubusercontent.com',
  'github-releases.githubusercontent.com',
  'release-assets.githubusercontent.com',
  'github.githubassets.com',
  'cdn.jsdelivr.net',
  'fastly.jsdelivr.net',
]);

function isAllowedRedirectHost(urlStr) {
  try {
    const parsed = new URL(urlStr);
    return (parsed.protocol === 'http:' || parsed.protocol === 'https:') &&
      (ALLOWED_REDIRECT_HOSTS.has(parsed.hostname) || parsed.hostname.endsWith('.github.io'));
  } catch {
    return false;
  }
}

function envWithDeprecatedAlias(primary, deprecatedName) {
  return process.env[primary] || process.env[deprecatedName] || '';
}

function applyMirror(canonicalUrl) {
  const mirror = envWithDeprecatedAlias('DBGOV_DOWNLOAD_MIRROR', 'DBGOV_CLI_DOWNLOAD_MIRROR');
  if (!mirror) return canonicalUrl;
  return mirror.replace(/\/+$/, '') + '/' + canonicalUrl;
}

function pickClient(url) {
  const protocol = new URL(url).protocol;
  if (protocol === 'http:') return http;
  if (protocol === 'https:') return https;
  throw new Error(`Unsupported download protocol: ${protocol}`);
}

function getPlatform() {
  const platformMap = { win32: 'windows', darwin: 'darwin', linux: 'linux' };
  const archMap = { x64: 'amd64', arm64: 'arm64' };
  return {
    os: platformMap[process.platform] || process.platform,
    arch: archMap[process.arch] || process.arch,
  };
}

function getBinaryName() {
  const { os, arch } = getPlatform();
  const ext = os === 'windows' ? '.exe' : '';
  return `dbgov-cli-${os}-${arch}${ext}`;
}

function getDownloadUrl() {
  const binary = getBinaryName();
  return applyMirror(`https://github.com/${REPO}/releases/download/v${VERSION}/${binary}`);
}

function request(url, onResponse, idleTimeoutMs = IDLE_TIMEOUT_MS) {
  let response;
  const req = pickClient(url).get(url, (value) => {
    response = value;
    onResponse(value);
  });
  req.setTimeout(idleTimeoutMs, () => {
    const err = new Error(`Download idle timeout after ${idleTimeoutMs / 1000}s`);
    if (response && !response.destroyed) response.destroy(err);
    req.destroy(err);
  });
  return req;
}

function redirectTarget(currentUrl, response) {
  if (!response.headers.location) {
    throw new Error('Redirect response is missing a Location header');
  }
  return new URL(response.headers.location, currentUrl).toString();
}

function download(url, temp) {
  return downloadWithLimits(url, temp);
}

function downloadWithLimits(url, temp, overrides = {}) {
  const policy = {
    clearTimer: overrides.clearTimer || clearTimeout,
    idleTimeoutMs: overrides.idleTimeoutMs ?? IDLE_TIMEOUT_MS,
    maxBytes: overrides.maxBytes ?? MAX_DOWNLOAD_BYTES,
    now: overrides.now || Date.now,
    setTimer: overrides.setTimer || setTimeout,
    totalTimeoutMs: overrides.totalTimeoutMs ?? TOTAL_TIMEOUT_MS,
  };
  const deadline = policy.now() + policy.totalTimeoutMs;

  return new Promise((resolve, reject) => {
    let activeRequest;
    let activeResponse;
    let settled = false;
    let pipelineActive = false;
    let pendingPipelineError;
    let downloadedBytes = 0;
    let totalTimer;

    const totalTimeoutError = () =>
      new Error(`Download exceeded the ${policy.totalTimeoutMs / 1000}s total deadline`);

    const finish = (err) => {
      if (settled) return;
      if (err && pipelineActive) {
        pendingPipelineError ||= err;
        if (activeResponse && !activeResponse.destroyed) activeResponse.destroy(err);
        if (activeRequest && !activeRequest.destroyed) activeRequest.destroy(err);
        return;
      }
      settled = true;
      if (totalTimer !== undefined) policy.clearTimer(totalTimer);
      if (err) {
        if (activeResponse && !activeResponse.destroyed) activeResponse.destroy(err);
        if (activeRequest && !activeRequest.destroyed) activeRequest.destroy(err);
        reject(err);
        return;
      }
      resolve();
    };

    const attempt = (currentUrl, redirectCount) => {
      if (settled) return;
      if (policy.now() >= deadline) {
        finish(totalTimeoutError());
        return;
      }
      if (redirectCount > MAX_REDIRECTS) {
        finish(new Error('Too many redirects'));
        return;
      }

      let req;
      let retired = false;
      const onRequestError = (err) => {
        if (!retired) finish(err);
      };
      const onResponse = (response) => {
        if (settled) {
          response.destroy();
          return;
        }
        activeResponse = response;

        if (response.statusCode === 301 || response.statusCode === 302 ||
            response.statusCode === 307 || response.statusCode === 308) {
          let target;
          try {
            target = redirectTarget(currentUrl, response);
          } catch (err) {
            finish(err);
            return;
          }
          if (!isAllowedRedirectHost(target)) {
            finish(new Error(`Redirect to non-allowed host rejected: ${target}`));
            return;
          }
          retired = true;
          response.destroy();
          if (activeRequest === req) activeRequest = undefined;
          if (activeResponse === response) activeResponse = undefined;
          attempt(target, redirectCount + 1);
          return;
        }

        if (response.statusCode !== 200) {
          finish(new Error(`Download failed: ${response.statusCode}`));
          return;
        }

        const lengthHeader = response.headers['content-length'];
        if (lengthHeader !== undefined) {
          const value = String(lengthHeader).trim();
          if (!/^(0|[1-9][0-9]*)$/.test(value)) {
            finish(new Error('Download has an invalid Content-Length header'));
            return;
          }
          const contentLength = Number(value);
          if (!Number.isSafeInteger(contentLength) || contentLength > policy.maxBytes) {
            finish(new Error(`Download exceeds the ${policy.maxBytes}-byte limit`));
            return;
          }
        }

        const limiter = new Transform({
          transform(chunk, encoding, callback) {
            downloadedBytes += chunk.length;
            if (downloadedBytes > policy.maxBytes) {
              callback(new Error(`Download exceeds the ${policy.maxBytes}-byte limit`));
              return;
            }
            callback(null, chunk);
          },
        });
        const file = fs.createWriteStream(temp.path, {
          fd: temp.fd,
          autoClose: false,
          start: 0,
        });
        pipelineActive = true;
        pipeline(response, limiter, file, (err) => {
          pipelineActive = false;
          const terminalError = pendingPipelineError || err;
          pendingPipelineError = undefined;
          if (err) {
            finish(terminalError);
            return;
          }
          activeRequest = undefined;
          activeResponse = undefined;
          finish(terminalError);
        });
      };
      try {
        req = request(currentUrl, onResponse, policy.idleTimeoutMs);
      } catch (err) {
        finish(err);
        return;
      }
      activeRequest = req;
      req.on('error', onRequestError);
    };

    const remaining = deadline - policy.now();
    if (remaining <= 0) {
      finish(totalTimeoutError());
      return;
    }
    totalTimer = policy.setTimer(() => finish(totalTimeoutError()), remaining);
    attempt(url, 0);
  });
}

function sha256File(filePath) {
  return new Promise((resolve, reject) => {
    const hash = crypto.createHash('sha256');
    const stream = fs.createReadStream(filePath);
    stream.on('data', (data) => hash.update(data));
    stream.on('end', () => resolve(hash.digest('hex')));
    stream.on('error', reject);
  });
}

function packageRepositoryURL(packageJson) {
  if (typeof packageJson.repository === 'string') {
    return packageJson.repository;
  }
  return packageJson.repository && packageJson.repository.url;
}

function expectedDigest(packageJson, binaryName) {
  if (packageJson.name !== PACKAGE_NAME ||
      packageJson.version !== VERSION ||
      packageRepositoryURL(packageJson) !== REPOSITORY_URL) {
    throw new Error('npm package identity does not match the trusted binary manifest');
  }

  const manifest = packageJson.binaryManifest;
  if (!manifest || typeof manifest !== 'object' || Array.isArray(manifest) ||
      manifest.schema !== 1 ||
      manifest.repository !== REPO ||
      manifest.version !== VERSION ||
      !manifest.binaries ||
      typeof manifest.binaries !== 'object' ||
      Array.isArray(manifest.binaries)) {
    throw new Error('npm package has no valid provenance-bound binary manifest');
  }

  const names = Object.keys(manifest.binaries).sort();
  const expectedNames = [...EXPECTED_BINARIES].sort();
  if (names.length !== expectedNames.length ||
      names.some((name, index) => name !== expectedNames[index])) {
    throw new Error('binary manifest must contain exactly the six supported release binaries');
  }
  for (const name of expectedNames) {
    if (!/^[a-f0-9]{64}$/.test(manifest.binaries[name])) {
      throw new Error(`binary manifest contains an invalid SHA-256 digest for ${name}`);
    }
  }
  if (!Object.prototype.hasOwnProperty.call(manifest.binaries, binaryName)) {
    throw new Error(`binary manifest has no exact entry for ${binaryName}`);
  }
  return manifest.binaries[binaryName];
}

function createExclusiveTemp(destination) {
  const directory = path.dirname(destination);
  const basename = path.basename(destination);
  for (let attempt = 0; attempt < 16; attempt += 1) {
    const suffix = crypto.randomBytes(16).toString('hex');
    const tempPath = path.join(directory, `.${basename}.${process.pid}.${suffix}.tmp`);
    try {
      const fd = fs.openSync(tempPath, 'wx', 0o600);
      return { path: tempPath, fd };
    } catch (err) {
      if (err.code !== 'EEXIST') throw err;
    }
  }
  throw new Error('Could not allocate an exclusive temporary download file');
}

function cleanupTemp(temp) {
  if (!temp) return;
  if (temp.fd !== null) {
    try {
      fs.closeSync(temp.fd);
    } catch {}
    temp.fd = null;
  }
  try {
    fs.unlinkSync(temp.path);
  } catch (err) {
    if (err.code !== 'ENOENT') throw err;
  }
}

async function installBinary(url, destination, binaryName, packageJson = pkg) {
  return installBinaryWithDownload(url, destination, binaryName, packageJson, download);
}

async function installBinaryWithDownload(url, destination, binaryName, packageJson, downloader) {
  const expected = expectedDigest(packageJson, binaryName);
  fs.mkdirSync(path.dirname(destination), { recursive: true });
  const temp = createExclusiveTemp(destination);
  let renamed = false;

  try {
    await downloader(url, temp);
    const actual = await sha256File(temp.path);
    if (!crypto.timingSafeEqual(Buffer.from(actual, 'hex'), Buffer.from(expected, 'hex'))) {
      throw new Error(
        `SHA-256 mismatch for ${binaryName}\n` +
        `  Expected: ${expected}\n` +
        `  Actual:   ${actual}\n` +
        'The downloaded binary may be corrupted or tampered with.'
      );
    }
    if (getPlatform().os !== 'windows') {
      fs.fchmodSync(temp.fd, 0o755);
    }
    fs.fsyncSync(temp.fd);
    fs.closeSync(temp.fd);
    temp.fd = null;
    fs.renameSync(temp.path, destination);
    renamed = true;
  } finally {
    if (!renamed) cleanupTemp(temp);
  }
}

async function main() {
  const { os, arch } = getPlatform();
  const binary = getBinaryName();
  const url = getDownloadUrl();
  const destDir = path.join(__dirname, '..', 'bin');
  const destination = path.join(destDir, os === 'windows' ? 'dbgov.exe' : 'dbgov');

  console.log(`Installing dbgov v${VERSION} for ${os}/${arch}...`);
  try {
    await installBinary(url, destination, binary);
    console.log('SHA-256 verification passed');
    console.log('dbgov installed successfully!');
  } catch (err) {
    console.error('Failed to install dbgov:', err.message);
    console.error('');
    console.error('Please download manually from:');
    console.error(`  ${url}`);
    process.exitCode = 1;
  }
}

if (require.main === module) {
  main();
}

module.exports = {
  _test: {
    ALLOWED_REDIRECT_HOSTS,
    EXPECTED_BINARIES,
    MAX_DOWNLOAD_BYTES,
    TOTAL_TIMEOUT_MS,
    createExclusiveTemp,
    download,
    downloadWithLimits,
    expectedDigest,
    installBinary,
    installBinaryWithDownload,
    request,
  },
};
