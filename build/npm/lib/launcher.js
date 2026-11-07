// Resolves, downloads and verifies the native lanesh binary.
//
// The npm package ships no binary. On first run it downloads the release
// binary that matches this package's version into a Lanesh-managed directory
// (~/.lanesh/bin by default), so that the daemon can later update itself
// without fighting npm over files in node_modules.

import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { chmodSync, existsSync, mkdirSync, renameSync, rmSync, writeFileSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';

export const DEFAULT_DOWNLOAD_BASE = 'https://github.com/jtsang4/lanesh/releases/download';

const OS_MAP = { darwin: 'darwin', linux: 'linux', win32: 'windows' };
const ARCH_MAP = { x64: 'amd64', arm64: 'arm64' };

/** Maps Node's platform/arch to Go's GOOS/GOARCH, or throws if unsupported. */
export function resolveTarget(platform = process.platform, arch = process.arch) {
  const os = OS_MAP[platform];
  const goarch = ARCH_MAP[arch];
  if (!os || !goarch) {
    throw new Error(`lanesh does not provide a binary for ${platform}/${arch}`);
  }
  return { os, arch: goarch, exe: os === 'windows' ? '.exe' : '' };
}

/** Name of the raw binary release asset, matching .goreleaser.yaml. */
export function assetName(version, target) {
  return `lanesh_${version}_${target.os}_${target.arch}${target.exe}`;
}

export function laneshHome(env = process.env) {
  return env.LANESH_HOME || join(homedir(), '.lanesh');
}

export function binaryPath(env = process.env, target = resolveTarget()) {
  return join(laneshHome(env), 'bin', `lanesh${target.exe}`);
}

function parseVersion(v) {
  const m = /^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?/.exec(String(v).trim());
  if (!m) return null;
  return { nums: [Number(m[1]), Number(m[2]), Number(m[3])], pre: m[4] ?? '' };
}

/** Compares two semantic versions. Unparseable versions sort lowest. */
export function compareVersions(a, b) {
  const pa = parseVersion(a);
  const pb = parseVersion(b);
  if (!pa || !pb) return pa ? 1 : pb ? -1 : 0;
  for (let i = 0; i < 3; i++) {
    if (pa.nums[i] !== pb.nums[i]) return pa.nums[i] < pb.nums[i] ? -1 : 1;
  }
  if (pa.pre === pb.pre) return 0;
  if (!pa.pre) return 1;
  if (!pb.pre) return -1;
  return pa.pre < pb.pre ? -1 : 1;
}

/** Returns true for versions that have no published release (local builds). */
export function isDevelopmentVersion(v) {
  return !parseVersion(v) || String(v).includes('development');
}

/** Extracts the sha256 for `name` from a goreleaser checksums.txt body. */
export function findChecksum(checksums, name) {
  for (const line of checksums.split('\n')) {
    const [sum, file] = line.trim().split(/\s+/);
    if (file === name) return sum.toLowerCase();
  }
  return null;
}

export function installedVersion(bin) {
  if (!existsSync(bin)) return null;
  try {
    return execFileSync(bin, ['version', '--short'], { encoding: 'utf8', timeout: 10_000 }).trim();
  } catch {
    return null;
  }
}

async function fetchOk(url) {
  const res = await fetch(url, { redirect: 'follow' });
  if (!res.ok) throw new Error(`download failed: ${url} (${res.status} ${res.statusText})`);
  return res;
}

/** Downloads, verifies and atomically installs the binary for `version`. */
export async function install(version, { env = process.env, target = resolveTarget() } = {}) {
  const base = (env.LANESH_DOWNLOAD_BASE || DEFAULT_DOWNLOAD_BASE).replace(/\/+$/, '');
  const name = assetName(version, target);
  const dir = `${base}/v${version}`;

  const [checksums, binary] = await Promise.all([
    fetchOk(`${dir}/checksums.txt`).then((r) => r.text()),
    fetchOk(`${dir}/${name}`).then(async (r) => Buffer.from(await r.arrayBuffer()))
  ]);

  const expected = findChecksum(checksums, name);
  if (!expected) throw new Error(`no checksum for ${name} in checksums.txt`);
  const actual = createHash('sha256').update(binary).digest('hex');
  if (actual !== expected) {
    throw new Error(`checksum mismatch for ${name}: expected ${expected}, got ${actual}`);
  }

  const bin = binaryPath(env, target);
  mkdirSync(join(laneshHome(env), 'bin'), { recursive: true });
  const tmp = `${bin}.download`;
  writeFileSync(tmp, binary, { mode: 0o755 });
  chmodSync(tmp, 0o755);

  if (target.os === 'windows' && existsSync(bin)) {
    // A running .exe cannot be overwritten, but it can be renamed.
    const old = `${bin}.old`;
    rmSync(old, { force: true });
    renameSync(bin, old);
  }
  renameSync(tmp, bin);
  return bin;
}

/**
 * Returns the path of a binary at least as new as `wanted`, downloading it if
 * needed. A newer managed binary (for example after a self-update) is kept.
 */
export async function ensureBinary(wanted, { env = process.env, target = resolveTarget() } = {}) {
  const bin = binaryPath(env, target);
  const current = installedVersion(bin);

  if (isDevelopmentVersion(wanted)) {
    if (current) return bin;
    throw new Error(
      `this is a development build of the lanesh npm package; install a release or place a binary at ${bin}`
    );
  }
  if (current && !isDevelopmentVersion(current) && compareVersions(current, wanted) >= 0) {
    return bin;
  }
  process.stderr.write(`lanesh: installing v${wanted} to ${bin}\n`);
  return install(wanted, { env, target });
}
