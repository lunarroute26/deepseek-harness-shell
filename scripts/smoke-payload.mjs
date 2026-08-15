#!/usr/bin/env node

import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { spawn, spawnSync } from 'node:child_process';

function fail(message) {
  console.error(`smoke-payload: ${message}`);
  process.exit(1);
}

function parseArgs(argv) {
  const result = { server: false };
  for (let index = 0; index < argv.length; index += 1) {
    if (argv[index] === '--server') {
      result.server = true;
    } else if (argv[index] === '--root' && argv[index + 1]) {
      result.root = argv[index + 1];
      index += 1;
    } else {
      fail(`invalid argument: ${argv[index]}`);
    }
  }
  if (!result.root) fail('missing --root');
  return result;
}

function run(node, args, expected, description) {
  const result = spawnSync(node, args, {
    encoding: 'utf8',
    timeout: 30_000,
    windowsHide: true,
  });
  if (result.error || result.status !== 0) {
    fail(`${description} failed: ${result.error?.message ?? result.stderr.trim()}`);
  }
  if (result.stdout.trim() !== expected) {
    fail(`${description} returned ${JSON.stringify(result.stdout.trim())}, expected ${JSON.stringify(expected)}`);
  }
}

function waitForReady(child, timeoutMs) {
  return new Promise((resolveReady, reject) => {
    let stdout = '';
    let stderr = '';
    const timer = setTimeout(() => {
      reject(new Error(`service did not become ready within ${timeoutMs / 1000}s\n${stdout}\n${stderr}`));
    }, timeoutMs);
    const finish = callback => value => {
      clearTimeout(timer);
      callback(value);
    };
    child.stdout.on('data', chunk => {
      stdout = `${stdout}${chunk}`.slice(-64 * 1024);
      const match = stdout.match(/http:\/\/127\.0\.0\.1:\d+/u);
      if (match) finish(resolveReady)(match[0]);
    });
    child.stderr.on('data', chunk => {
      stderr = `${stderr}${chunk}`.slice(-64 * 1024);
    });
    child.once('error', finish(reject));
    child.once('exit', (code, signal) => {
      finish(reject)(new Error(
        `service exited before readiness (code=${code}, signal=${signal})\n${stdout}\n${stderr}`,
      ));
    });
  });
}

async function stop(child) {
  if (child.exitCode !== null || child.signalCode !== null) return;
  const exited = new Promise(resolveExit => child.once('exit', resolveExit));
  child.kill('SIGTERM');
  const timer = new Promise(resolveTimer => setTimeout(resolveTimer, 5_000, 'timeout'));
  if (await Promise.race([exited, timer]) === 'timeout') child.kill('SIGKILL');
}

async function smokeServer(node, entry, dshRoot) {
  const home = mkdtempSync(join(tmpdir(), 'dsh-payload-smoke-'));
  const child = spawn(node, [
    entry,
    '--profile', 'web',
    '--host', '127.0.0.1',
    '--port', '0',
  ], {
    cwd: dshRoot,
    env: { ...process.env, DSH_HOME: home },
    stdio: ['ignore', 'pipe', 'pipe'],
    windowsHide: true,
  });
  try {
    const url = await waitForReady(child, 90_000);
    const response = await fetch(url, { signal: AbortSignal.timeout(10_000) });
    if (!response.ok || (await response.text()).length === 0) {
      throw new Error(`service health request failed: HTTP ${response.status}`);
    }
    console.log(`smoke-payload: local service ready at ${url}`);
  } finally {
    await stop(child);
    rmSync(home, { recursive: true, force: true });
  }
}

const args = parseArgs(process.argv.slice(2));
const root = resolve(args.root);
const manifest = JSON.parse(readFileSync(join(root, 'payload.json'), 'utf8'));
const node = join(root, 'runtime', 'bin', manifest.platform === 'windows' ? 'node.exe' : 'node');
const entry = join(root, 'dsh', 'lib', 'bin.js');

run(node, ['--version'], manifest.node, 'bundled Node');
run(node, [entry, '--version'], manifest.dsh, 'bundled dsh');

if (args.server) await smokeServer(node, entry, dirname(dirname(entry)));
console.log(`smoke-payload: valid runtime ${manifest.platform}/${manifest.arch}`);
