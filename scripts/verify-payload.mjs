#!/usr/bin/env node

import {
  existsSync,
  lstatSync,
  readFileSync,
  readlinkSync,
  readdirSync,
  realpathSync,
} from 'node:fs';
import { dirname, isAbsolute, join, relative, resolve, sep } from 'node:path';
import { executableTarget, windowsPESubsystem } from './payload-executable.mjs';
import {
  invalidUnixSpawnHelpers,
  missingNativeRuntimeFiles,
  unexpectedNativeDirectories,
} from './payload-native.mjs';
import { windowsSubprocessVisibilityViolations } from './payload-windows.mjs';

function fail(message) {
  console.error(`verify-payload: ${message}`);
  process.exit(1);
}

function parseArgs(argv) {
  const values = {};
  for (let index = 0; index < argv.length; index += 2) {
    const key = argv[index];
    const value = argv[index + 1];
    if (!key?.startsWith('--') || value === undefined) fail(`invalid argument: ${key ?? ''}`);
    values[key.slice(2)] = value;
  }
  for (const key of ['root', 'platform', 'arch']) {
    if (!values[key]) fail(`missing --${key}`);
  }
  return values;
}

function isWithin(path, parent) {
  const rel = relative(parent, path);
  return rel === '' || (!rel.startsWith(`..${sep}`) && rel !== '..' && !isAbsolute(rel));
}

const args = parseArgs(process.argv.slice(2));
const root = realpathSync(resolve(args.root));
const nodeName = args.platform === 'windows' ? 'node.exe' : 'node';
const required = [
  'payload.json',
  join('runtime', 'bin', nodeName),
  join('dsh', 'package.json'),
  join('dsh', 'pnpm-lock.yaml'),
  join('dsh', 'lib', 'bin.js'),
];

for (const path of required) {
  if (!existsSync(join(root, path))) fail(`missing required file: ${path}`);
}

const nodePath = join(root, 'runtime', 'bin', nodeName);
const nodeTarget = executableTarget(nodePath);
if (nodeTarget?.platform !== args.platform || nodeTarget.arch !== args.arch) {
  const actual = nodeTarget ? `${nodeTarget.platform}/${nodeTarget.arch}` : 'unknown';
  fail(`Node executable targets ${actual}, expected ${args.platform}/${args.arch}`);
}
if (args.platform === 'windows' && windowsPESubsystem(nodePath) !== 2) {
  fail('bundled Node executable is not a Windows GUI process');
}

let manifest;
try {
  manifest = JSON.parse(readFileSync(join(root, 'payload.json'), 'utf8'));
} catch (error) {
  fail(`invalid payload.json: ${error.message}`);
}
if (manifest.platform !== args.platform || manifest.arch !== args.arch) {
  fail(`manifest target is ${manifest.platform}/${manifest.arch}, expected ${args.platform}/${args.arch}`);
}
if (!manifest.node || !manifest.dsh || !manifest.dshCommit) {
  fail('payload.json is missing node, dsh, or dshCommit metadata');
}
if (manifest.deployment !== 'pnpm-lockfile') {
  fail(`payload uses ${manifest.deployment ?? 'unknown'} deployment mode, expected pnpm-lockfile`);
}
if (!Number.isSafeInteger(manifest.nativePruning?.removedDirectories)
  || !Number.isSafeInteger(manifest.nativePruning?.removedBytes)
  || !Number.isSafeInteger(manifest.nativeHydration)
  || !Number.isSafeInteger(manifest.executableRepairs)) {
  fail('payload.json is missing native pruning metadata');
}

const unexpectedNative = unexpectedNativeDirectories(join(root, 'dsh'), args.platform, args.arch);
if (unexpectedNative.length > 0) {
  fail(`payload contains native directories for another target: ${unexpectedNative.join(', ')}`);
}
const missingNative = missingNativeRuntimeFiles(join(root, 'dsh'), args.platform, args.arch);
if (missingNative.length > 0) {
  fail(`payload is missing target native runtime files: ${missingNative.join(', ')}`);
}
const invalidHelpers = invalidUnixSpawnHelpers(join(root, 'dsh'), args.platform, args.arch);
if (invalidHelpers.length > 0) {
  fail(`payload contains missing or non-executable node-pty spawn-helper files: ${invalidHelpers.join(', ')}`);
}

const visit = directory => {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    const stat = lstatSync(path);
    if (stat.isSymbolicLink()) {
      if (args.platform === 'windows') fail(`Windows payload contains a link: ${path}`);
      const target = resolve(dirname(path), readlinkSync(path));
      if (!isWithin(target, root)) fail(`external link: ${path} -> ${target}`);
    } else if (stat.isDirectory()) {
      visit(path);
    }
  }
};
visit(root);

if (args.platform === 'windows' && manifest.dependencyLayout !== 'hoisted') {
  fail(`Windows payload uses ${manifest.dependencyLayout ?? 'unknown'} dependency layout, expected hoisted`);
}
if (args.platform === 'windows') {
  if (manifest.nodeProcessType !== 'windows-gui' || manifest.windowsSubprocessRepairs !== 2) {
    fail('Windows payload is missing windowless-process metadata');
  }
  const violations = windowsSubprocessVisibilityViolations(root);
  if (violations.length > 0) fail(violations.join('; '));
}

console.log(`verify-payload: valid ${args.platform}/${args.arch} payload at ${root}`);
