import assert from 'node:assert/strict';
import {
  existsSync,
  lstatSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from 'node:fs';
import { spawnSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { materializeFileLinks, walkLinks } from './payload-links.mjs';

test('materializes file links for a physical Windows payload', () => {
  const root = mkdtempSync(join(tmpdir(), 'dsh-payload-links-'));
  try {
    const target = join(root, 'package', 'cli.js');
    const link = join(root, 'node_modules', '.bin', 'cli');
    mkdirSync(join(root, 'package'), { recursive: true });
    mkdirSync(join(root, 'node_modules', '.bin'), { recursive: true });
    writeFileSync(target, 'console.log("ok")\n', { mode: 0o755 });
    symlinkSync(target, link, 'file');

    assert.deepEqual(walkLinks(root), [link]);
    assert.equal(materializeFileLinks(root), 1);
    assert.deepEqual(walkLinks(root), []);
    assert.equal(lstatSync(link).isSymbolicLink(), false);
    assert.equal(readFileSync(link, 'utf8'), 'console.log("ok")\n');
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('rejects directory links instead of recursively copying a dependency cycle', () => {
  const root = mkdtempSync(join(tmpdir(), 'dsh-payload-links-'));
  try {
    const target = join(root, 'package');
    const link = join(root, 'node_modules', 'package');
    mkdirSync(target, { recursive: true });
    mkdirSync(join(root, 'node_modules'), { recursive: true });
    symlinkSync(target, link, process.platform === 'win32' ? 'junction' : 'dir');

    assert.throws(() => materializeFileLinks(root), /directory link remains/);
    assert.equal(existsSync(link), true);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('payload verification rejects every link in a Windows payload', () => {
  const root = mkdtempSync(join(tmpdir(), 'dsh-windows-payload-'));
  try {
    const dshRoot = join(root, 'dsh');
    mkdirSync(join(root, 'runtime', 'bin'), { recursive: true });
    mkdirSync(join(dshRoot, 'lib'), { recursive: true });
    mkdirSync(join(dshRoot, 'node_modules'), { recursive: true });
    const node = Buffer.alloc(256);
    node.write('MZ');
    node.writeUInt32LE(0x80, 0x3c);
    node.write('PE\0\0', 0x80, 'binary');
    node.writeUInt16LE(0x8664, 0x84);
    writeFileSync(join(root, 'runtime', 'bin', 'node.exe'), node);
    writeFileSync(join(dshRoot, 'package.json'), '{}\n');
    writeFileSync(join(dshRoot, 'pnpm-lock.yaml'), 'lockfileVersion: 9.0\n');
    writeFileSync(join(dshRoot, 'lib', 'bin.js'), '');
    writeFileSync(join(root, 'payload.json'), `${JSON.stringify({
      platform: 'windows',
      arch: 'amd64',
      node: 'v24.0.0',
      dsh: 'test',
      dshCommit: 'test',
      deployment: 'pnpm-lockfile',
      dependencyLayout: 'hoisted',
      materializedLinks: 0,
      nativeHydration: 0,
      executableRepairs: 0,
      nativePruning: { removedDirectories: 0, removedBytes: 0 },
    })}\n`);
    symlinkSync(
      join(dshRoot, 'lib'),
      join(dshRoot, 'node_modules', 'cycle'),
      process.platform === 'win32' ? 'junction' : 'dir',
    );

    const result = spawnSync(process.execPath, [
      fileURLToPath(new URL('./verify-payload.mjs', import.meta.url)),
      '--root', root,
      '--platform', 'windows',
      '--arch', 'amd64',
    ], { encoding: 'utf8' });
    assert.equal(result.status, 1);
    assert.match(result.stderr, /Windows payload contains a link/);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
