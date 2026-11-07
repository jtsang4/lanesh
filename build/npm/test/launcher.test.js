import assert from 'node:assert/strict';
import { join } from 'node:path';
import { test } from 'node:test';

import {
  assetName,
  binaryPath,
  compareVersions,
  findChecksum,
  isDevelopmentVersion,
  resolveTarget
} from '../lib/launcher.js';

test('resolveTarget maps Node platforms to Go targets', () => {
  assert.deepEqual(resolveTarget('darwin', 'arm64'), { os: 'darwin', arch: 'arm64', exe: '' });
  assert.deepEqual(resolveTarget('linux', 'x64'), { os: 'linux', arch: 'amd64', exe: '' });
  assert.deepEqual(resolveTarget('win32', 'x64'), { os: 'windows', arch: 'amd64', exe: '.exe' });
  assert.throws(() => resolveTarget('aix', 'ppc64'));
});

test('assetName matches the goreleaser binary archive naming', () => {
  assert.equal(assetName('1.2.3', resolveTarget('linux', 'arm64')), 'lanesh_1.2.3_linux_arm64');
  assert.equal(
    assetName('1.2.3', resolveTarget('win32', 'arm64')),
    'lanesh_1.2.3_windows_arm64.exe'
  );
});

test('binaryPath honours LANESH_HOME', () => {
  const target = resolveTarget('linux', 'x64');
  assert.equal(binaryPath({ LANESH_HOME: '/opt/lanesh' }, target), join('/opt/lanesh', 'bin', 'lanesh'));
});

test('compareVersions orders semantic versions', () => {
  assert.equal(compareVersions('1.2.3', '1.2.3'), 0);
  assert.equal(compareVersions('1.2.3', '1.10.0'), -1);
  assert.equal(compareVersions('v2.0.0', '1.9.9'), 1);
  assert.equal(compareVersions('1.0.0-rc.1', '1.0.0'), -1);
  assert.equal(compareVersions('1.0.0-rc.10', '1.0.0-rc.9'), 1);
  assert.equal(compareVersions('dev', '0.1.0'), -1);
});

test('isDevelopmentVersion detects unreleased builds', () => {
  assert.equal(isDevelopmentVersion('0.0.0-development'), true);
  assert.equal(isDevelopmentVersion('dev'), true);
  assert.equal(isDevelopmentVersion('1.0.0'), false);
});

test('findChecksum reads goreleaser checksums.txt', () => {
  const body = 'abc123  lanesh_1.0.0_linux_amd64\nDEF456  lanesh_1.0.0_darwin_arm64\n';
  assert.equal(findChecksum(body, 'lanesh_1.0.0_darwin_arm64'), 'def456');
  assert.equal(findChecksum(body, 'missing'), null);
});
