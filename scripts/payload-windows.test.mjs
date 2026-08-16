import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import {
  hardenWindowsSubprocessRuntime,
  windowsSubprocessVisibilityViolations,
} from './payload-windows.mjs';

test('hardens every bundled Windows child-process path', () => {
  const root = mkdtempSync(join(tmpdir(), 'dsh-payload-windows-'));
  const runtime = join(
    root, 'dsh', 'node_modules', '@deepseek-ai', 'dsh-subprocess-local', 'lib', 'index.js',
  );
  try {
    mkdirSync(join(runtime, '..'), { recursive: true });
    writeFileSync(runtime, [
      'spawnSync("taskkill", ["/PID", String(pid), "/T", "/F"], { stdio: "ignore" });',
      'const child = spawn(program, args, {',
      '\tdetached: platform !== "win32",',
      '});',
    ].join('\n'));

    assert.equal(hardenWindowsSubprocessRuntime(root), 2);
    assert.deepEqual(windowsSubprocessVisibilityViolations(root), []);
    assert.match(readFileSync(runtime, 'utf8'), /windowsHide: true/u);
    assert.match(readFileSync(runtime, 'utf8'), /windowsHide: platform === "win32"/u);
    assert.equal(hardenWindowsSubprocessRuntime(root), 2, 'hardening must be idempotent');
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
