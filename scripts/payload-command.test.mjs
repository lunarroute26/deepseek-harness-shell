import assert from 'node:assert/strict';
import test from 'node:test';
import { commandInvocation } from './payload-command.mjs';

test('runs the Windows pnpm shim through cmd.exe', () => {
  assert.deepEqual(
    commandInvocation('pnpm', ['deploy', '--prod'], {
      platform: 'win32',
      comspec: 'C:\\Windows\\System32\\cmd.exe',
    }),
    {
      executable: 'C:\\Windows\\System32\\cmd.exe',
      args: ['/d', '/s', '/c', 'pnpm.cmd', 'deploy', '--prod'],
    },
  );
});

test('runs native commands directly on Unix', () => {
  assert.deepEqual(
    commandInvocation('pnpm', ['deploy'], { platform: 'linux' }),
    { executable: 'pnpm', args: ['deploy'] },
  );
});
