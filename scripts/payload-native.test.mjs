import assert from 'node:assert/strict';
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  rmSync,
  statSync,
  symlinkSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import {
  invalidUnixSpawnHelpers,
  hydrateLinuxNodePtyBuild,
  missingNativeRuntimeFiles,
  pruneNativePayload,
  repairUnixSpawnHelpers,
  unexpectedNativeDirectories,
} from './payload-native.mjs';

const PREBUILDS = ['darwin-arm64', 'darwin-x64', 'win32-arm64', 'win32-x64'];
const CONPTY_ARCHES = ['win10-arm64', 'win10-x64'];

function fixture() {
  const root = mkdtempSync(join(tmpdir(), 'dsh-payload-native-'));
  const packageRoot = join(root, 'node_modules', '.pnpm', 'node-pty@1.1.0', 'node_modules', 'node-pty');
  for (const target of PREBUILDS) {
    const directory = join(packageRoot, 'prebuilds', target);
    mkdirSync(directory, { recursive: true });
    writeFileSync(join(directory, 'pty.node'), target);
    if (target.startsWith('darwin-')) writeFileSync(join(directory, 'spawn-helper'), target, { mode: 0o644 });
  }
  for (const target of CONPTY_ARCHES) {
    const directory = join(packageRoot, 'third_party', 'conpty', '1.0.0', target);
    mkdirSync(directory, { recursive: true });
    writeFileSync(join(directory, 'conpty.dll'), target);
  }
  return root;
}

function runCase(platform, arch, keptPrebuild, keptConpty, removedDirectories) {
  const root = fixture();
  try {
    assert.ok(unexpectedNativeDirectories(root, platform, arch).length > 0);
    const result = pruneNativePayload(root, platform, arch);
    assert.equal(result.removedDirectories, removedDirectories);
    assert.ok(result.removedBytes > 0);
    assert.deepEqual(unexpectedNativeDirectories(root, platform, arch), []);

    const packageRoot = join(root, 'node_modules', '.pnpm', 'node-pty@1.1.0', 'node_modules', 'node-pty');
    for (const target of PREBUILDS) {
      assert.equal(
        existsSync(join(packageRoot, 'prebuilds', target)),
        target === keptPrebuild,
        `unexpected prebuild state for ${target}`,
      );
    }
    for (const target of CONPTY_ARCHES) {
      assert.equal(
        existsSync(join(packageRoot, 'third_party', 'conpty', '1.0.0', target)),
        target === keptConpty,
        `unexpected conpty state for ${target}`,
      );
    }
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
}

test('Darwin arm64 payload keeps only the Darwin arm64 node-pty prebuild', () => {
  runCase('darwin', 'arm64', 'darwin-arm64', undefined, 5);
});

test('Windows amd64 payload keeps only the Windows x64 node-pty assets', () => {
  runCase('windows', 'amd64', 'win32-x64', 'win10-x64', 4);
});

test('Linux amd64 payload removes macOS and Windows node-pty assets', () => {
  runCase('linux', 'amd64', undefined, undefined, 6);
});

test('repairs and verifies the Darwin node-pty spawn-helper mode', {
  skip: process.platform === 'win32',
}, () => {
  const root = fixture();
  try {
    pruneNativePayload(root, 'darwin', 'arm64');
    assert.equal(invalidUnixSpawnHelpers(root, 'darwin', 'arm64').length, 1);
    assert.equal(repairUnixSpawnHelpers(root, 'darwin', 'arm64'), 1);
    assert.deepEqual(invalidUnixSpawnHelpers(root, 'darwin', 'arm64'), []);
    const helper = join(
      root,
      'node_modules', '.pnpm', 'node-pty@1.1.0', 'node_modules', 'node-pty',
      'prebuilds', 'darwin-arm64', 'spawn-helper',
    );
    assert.notEqual(statSync(helper).mode & 0o111, 0);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('hydrates the Linux node-pty native build through a linked pnpm virtual store', {
  skip: process.platform === 'win32',
}, () => {
  const root = mkdtempSync(join(tmpdir(), 'dsh-payload-linux-native-'));
  try {
    const sourceModules = join(root, 'source', 'node_modules');
    const virtualStore = join(sourceModules, '.pnpm');
    const storePackage = join(root, 'store', 'node-pty@1.1.0', 'node_modules', 'node-pty');
    const sourceRelease = join(storePackage, 'build', 'Release');
    const payload = join(root, 'payload');
    const payloadPackage = join(payload, 'node_modules', 'node-pty');
    mkdirSync(sourceRelease, { recursive: true });
    mkdirSync(virtualStore, { recursive: true });
    mkdirSync(payloadPackage, { recursive: true });
    symlinkSync(join(root, 'store', 'node-pty@1.1.0'), join(virtualStore, 'node-pty@1.1.0'));
    writeFileSync(join(sourceRelease, 'pty.node'), 'linux-x64');

    assert.equal(hydrateLinuxNodePtyBuild(payload, sourceModules, 'linux'), 1);
    assert.deepEqual(missingNativeRuntimeFiles(payload, 'linux', 'amd64'), []);
    assert.equal(repairUnixSpawnHelpers(payload, 'linux', 'amd64'), 0);
    assert.deepEqual(invalidUnixSpawnHelpers(payload, 'linux', 'amd64'), []);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
