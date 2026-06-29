#!/usr/bin/env node

const fs = require('fs');
const path = require('path');
const https = require('https');
const http = require('http');
const crypto = require('crypto');
const { URL } = require('url');
const { createWriteStream } = require('fs');

const pkg = require('../package.json');
const VERSION = pkg.version;
const REPO = 'JiangHe12/dbgov-cli';

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
    if (ALLOWED_REDIRECT_HOSTS.has(parsed.hostname)) return true;
    if (parsed.hostname.endsWith('.github.io')) return true;
    return false;
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
  return new URL(url).protocol === 'http:' ? http : https;
}

function getPlatform() {
  const platform = process.platform;
  const arch = process.arch;

  const platformMap = {
    'win32': 'windows',
    'darwin': 'darwin',
    'linux': 'linux'
  };

  const archMap = {
    'x64': 'amd64',
    'arm64': 'arm64'
  };

  return {
    os: platformMap[platform] || platform,
    arch: archMap[arch] || arch
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

function download(url, dest, redirectCount = 0) {
  return new Promise((resolve, reject) => {
    if (redirectCount > 5) {
      reject(new Error('Too many redirects'));
      return;
    }
    const file = createWriteStream(dest);

    pickClient(url).get(url, { timeout: 30000 }, (response) => {
      if (response.statusCode === 302 || response.statusCode === 301) {
        const target = response.headers.location;
        if (!isAllowedRedirectHost(target)) {
          reject(new Error(`Redirect to non-allowed host rejected: ${target}`));
          return;
        }
        download(target, dest, redirectCount + 1).then(resolve).catch(reject);
        return;
      }

      if (response.statusCode !== 200) {
        reject(new Error(`Download failed: ${response.statusCode}`));
        return;
      }

      response.pipe(file);
      file.on('finish', () => {
        file.close();
        resolve();
      });
    }).on('error', (err) => {
      fs.unlink(dest, () => {});
      reject(err);
    });
  });
}

function getChecksumsUrl() {
  return `https://github.com/${REPO}/releases/download/v${VERSION}/checksums.txt`;
}

function downloadToString(url, maxRedirects = 5) {
  return new Promise((resolve, reject) => {
    pickClient(url).get(url, { timeout: 30000 }, (response) => {
      if (response.statusCode === 302 || response.statusCode === 301) {
        if (maxRedirects <= 0) {
          reject(new Error('Too many redirects'));
          return;
        }
        downloadToString(response.headers.location, maxRedirects - 1).then(resolve).catch(reject);
        return;
      }

      if (response.statusCode !== 200) {
        reject(new Error(`Download failed: ${response.statusCode}`));
        return;
      }

      let data = '';
      response.on('data', (chunk) => {
        data += chunk;
      });
      response.on('end', () => {
        resolve(data);
      });
    }).on('error', reject);
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

function parseChecksums(text) {
  const checksums = {};
  for (const line of text.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    const match = trimmed.match(/^([a-f0-9]{64})\s+\*?(.+)$/);
    if (match) {
      checksums[match[2]] = match[1];
    }
  }
  return checksums;
}

async function verifyDownloadedBinary(binaryPath, binaryName) {
  if (envWithDeprecatedAlias('DBGOV_SKIP_VERIFY', 'DBGOV_CLI_SKIP_VERIFY') === '1') {
    console.log('Verification skipped (DBGOV_SKIP_VERIFY=1)');
    return;
  }

  const checksumsUrl = getChecksumsUrl();
  let checksums = {};
  try {
    const checksumsText = await downloadToString(checksumsUrl);
    checksums = parseChecksums(checksumsText);
  } catch (err) {
    throw new Error(
      `Could not fetch canonical checksums.txt from ${checksumsUrl}: ${err.message}. ` +
      'Set DBGOV_SKIP_VERIFY=1 to install without checksum verification.'
    );
  }

  if (!checksums[binaryName]) {
    throw new Error(
      `No checksum found for ${binaryName}. ` +
      'Set DBGOV_SKIP_VERIFY=1 to install without checksum verification.'
    );
  }

  const expected = checksums[binaryName];
  const actual = await sha256File(binaryPath);

  if (actual !== expected) {
    try { fs.unlinkSync(binaryPath); } catch (e) {}
    throw new Error(
      `SHA-256 mismatch for ${binaryName}\n` +
      `  Expected: ${expected}\n` +
      `  Actual:   ${actual}\n` +
      `The downloaded binary may be corrupted or tampered with.`
    );
  }

  console.log('SHA-256 verification passed');
}

async function main() {
  const { os, arch } = getPlatform();
  const binary = getBinaryName();
  const url = getDownloadUrl();
  const destDir = path.join(__dirname, '..', 'bin');
  const dest = path.join(destDir, os === 'windows' ? 'dbgov.exe' : 'dbgov');

  console.log(`Installing dbgov v${VERSION} for ${os}/${arch}...`);

  if (!fs.existsSync(destDir)) {
    fs.mkdirSync(destDir, { recursive: true });
  }

  try {
    await download(url, dest);
    await verifyDownloadedBinary(dest, binary);

    if (os !== 'windows') {
      fs.chmodSync(dest, 0o755);
    }

    console.log('dbgov installed successfully!');
  } catch (err) {
    console.error('Failed to install dbgov:', err.message);
    console.error('');
    console.error('Please download manually from:');
    console.error(`  ${url}`);
    process.exit(1);
  }
}

main();
