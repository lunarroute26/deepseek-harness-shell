import assert from 'node:assert/strict';
import { mkdtempSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { executableTarget, resolveExecutablePath } from './payload-executable.mjs';

function withFixture(buffer, callback) {
  const root = mkdtempSync(join(tmpdir(), 'dsh-payload-executable-'));
  try {
    const path = join(root, 'node');
    writeFileSync(path, buffer);
    callback(path);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
}

test('detects Windows amd64 PE executables', () => {
  const buffer = Buffer.alloc(256);
  buffer.write('MZ');
  buffer.writeUInt32LE(0x80, 0x3c);
  buffer.write('PE\0\0', 0x80, 'binary');
  buffer.writeUInt16LE(0x8664, 0x84);
  withFixture(buffer, path => {
    assert.deepEqual(executableTarget(path), { platform: 'windows', arch: 'amd64' });
  });
});

test('detects Linux arm64 ELF executables', () => {
  const buffer = Buffer.alloc(64);
  buffer.set([0x7f, 0x45, 0x4c, 0x46, 2, 1]);
  buffer.writeUInt16LE(0xb7, 18);
  withFixture(buffer, path => {
    assert.deepEqual(executableTarget(path), { platform: 'linux', arch: 'arm64' });
  });
});

test('detects Darwin arm64 Mach-O executables', () => {
  const buffer = Buffer.alloc(64);
  buffer.writeUInt32LE(0xfeedfacf, 0);
  buffer.writeUInt32LE(0x0100000c, 4);
  withFixture(buffer, path => {
    assert.deepEqual(executableTarget(path), { platform: 'darwin', arch: 'arm64' });
  });
});

test('resolves extensionless Windows executable paths', () => {
  const root = mkdtempSync(join(tmpdir(), 'dsh-payload-node-path-'));
  try {
    const executable = join(root, 'node.exe');
    writeFileSync(executable, 'node');
    assert.equal(resolveExecutablePath(join(root, 'node'), 'win32'), realpathSync(executable));
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
