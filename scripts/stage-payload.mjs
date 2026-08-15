#!/usr/bin/env node

import {
  chmodSync,
  copyFileSync,
  cpSync,
  existsSync,
  lstatSync,
  mkdirSync,
  readFileSync,
  readlinkSync,
  readdirSync,
  realpathSync,
  rmSync,
  statSync,
  symlinkSync,
  writeFileSync,
} from 'node:fs';
import { dirname, isAbsolute, join, relative, resolve, sep } from 'node:path';
import { spawnSync } from 'node:child_process';
import { materializeFileLinks, walkLinks } from './payload-links.mjs';
import { pruneNativePayload, repairUnixSpawnHelpers } from './payload-native.mjs';

function fail(message) {
  console.error(`stage-payload: ${message}`);
  process.exit(1);
}

function parseArgs(argv) {
  const values = {};
  for (let index = 0; index < argv.length; index += 1) {
    const key = argv[index];
    if (!key.startsWith('--') || index + 1 >= argv.length) fail(`invalid argument: ${key}`);
    values[key.slice(2)] = argv[index + 1];
    index += 1;
  }
  for (const key of ['repo', 'node', 'out', 'platform', 'arch']) {
    if (!values[key]) fail(`missing --${key}`);
  }
  return values;
}

function run(command, args, options = {}) {
  const executable = process.platform === 'win32' && command === 'pnpm' ? 'pnpm.cmd' : command;
  const result = spawnSync(executable, args, { stdio: 'inherit', ...options });
  if (result.error) fail(`${command} failed: ${result.error.message}`);
  if (result.status !== 0) fail(`${command} exited with status ${result.status}`);
}

function capture(command, args, options = {}) {
  const result = spawnSync(command, args, { encoding: 'utf8', ...options });
  if (result.error || result.status !== 0) return '';
  return result.stdout.trim();
}

function isWithin(path, parent) {
  const rel = relative(parent, path);
  return rel === '' || (!rel.startsWith(`..${sep}`) && rel !== '..' && !isAbsolute(rel));
}

function workspacePackages(repo) {
  const result = new Map();
  const roots = [join(repo, 'vendor'), join(repo, 'apps'), join(repo, 'packages')];
  for (const root of roots) {
    if (!existsSync(root)) continue;
    for (const first of readdirSync(root, { withFileTypes: true })) {
      if (!first.isDirectory()) continue;
      const firstDir = join(root, first.name);
      const candidates = [firstDir];
      if (root.endsWith(`${sep}packages`)) {
        candidates.splice(0, 1, ...readdirSync(firstDir, { withFileTypes: true })
          .filter(entry => entry.isDirectory())
          .map(entry => join(firstDir, entry.name)));
      }
      for (const directory of candidates) {
        const manifestPath = join(directory, 'package.json');
        if (!existsSync(manifestPath)) continue;
        const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'));
        if (typeof manifest.name === 'string') result.set(manifest.name, { directory, manifest });
      }
    }
  }
  return result;
}

function runtimeWorkspaceClosure(packages, rootName) {
  const closure = new Set();
  const queue = [rootName];
  while (queue.length > 0) {
    const name = queue.shift();
    if (closure.has(name)) continue;
    closure.add(name);
    const entry = packages.get(name);
    if (!entry) continue;
    const manifest = entry.manifest;
    for (const section of ['dependencies', 'optionalDependencies']) {
      for (const dependency of Object.keys(manifest[section] ?? {})) {
        if (packages.has(dependency)) queue.push(dependency);
      }
    }
    for (const peer of Object.keys(manifest.peerDependencies ?? {})) {
      if (manifest.peerDependenciesMeta?.[peer]?.optional !== true && packages.has(peer)) queue.push(peer);
    }
  }
  return closure;
}

function linkInside(link, target) {
  if (existsSync(link) || lstatExists(link)) return;
  mkdirSync(dirname(link), { recursive: true });
  const value = process.platform === 'win32' ? target : relative(dirname(link), target);
  symlinkSync(value, link, statSync(target).isDirectory() ? 'junction' : 'file');
}

function lstatExists(path) {
  try {
    lstatSync(path);
    return true;
  } catch {
    return false;
  }
}

function packagePath(root, name) {
  return join(root, ...name.split('/'));
}

function copyRuntimePackage(source, destination) {
  cpSync(source, destination, {
    recursive: true,
    dereference: true,
    filter: path => {
      const first = relative(source, path).split(sep)[0];
      return !['node_modules', 'tests', '.git'].includes(first);
    },
  });
}

const args = parseArgs(process.argv.slice(2));
const repo = realpathSync(resolve(args.repo));
const nodeSource = realpathSync(resolve(args.node));
const output = resolve(args.out);
const dshOutput = join(output, 'dsh');
const runtimeBin = join(output, 'runtime', 'bin');
const nodeName = args.platform === 'windows' ? 'node.exe' : 'node';
const physicalDependencyLayout = args.platform === 'windows';

for (const required of [
  join(repo, 'apps', 'cli', 'lib', 'bin.js'),
  join(repo, 'apps', 'web', 'dist', 'index.html'),
]) {
  if (!existsSync(required)) fail(`required dsh build output is missing: ${required}`);
}

rmSync(output, { recursive: true, force: true });
mkdirSync(output, { recursive: true });

run('pnpm', [
  ...(physicalDependencyLayout ? ['--config.node-linker=hoisted'] : []),
  '--config.inject-workspace-packages=true',
  // pnpm currently fails strict deploy for an allowlisted workspace
  // postinstall. Disabling strictness only skips unapproved scripts; it does
  // not permit them to run. stage-payload applies that package's reviewed
  // spawn-helper chmod explicitly after pruning native assets.
  '--config.strict-dep-builds=false',
  '--dir', repo,
  '--filter', '@deepseek-ai/dsh',
  'deploy', '--prod', '--frozen-lockfile', dshOutput,
]);

// Keep compatibility with source link: overrides and older pnpm stores by
// internalising any workspace links that remain after deploy.
const sourceMappings = [
  [join(repo, 'apps', 'cli'), dshOutput],
  [join(repo, 'vendor', 'cosmokit'), join(dshOutput, 'vendor', 'cosmokit')],
  [join(repo, 'vendor', 'schemastery'), join(dshOutput, 'vendor', 'schemastery')],
  [join(repo, 'native', 'landlock-run', 'packages'), join(dshOutput, 'native', 'landlock-run', 'packages')],
];

for (const [source, destination] of sourceMappings.slice(1)) {
  if (!physicalDependencyLayout && existsSync(source)) {
    cpSync(source, destination, {
      recursive: true,
      dereference: true,
      filter: path => relative(source, path).split(sep)[0] !== 'node_modules',
    });
  }
}

// Expose pnpm's transitive store as a flat fallback. dsh profile plugins are
// loaded from a writable directory, so their required peers must be reachable
// from a stable installation-level node_modules path.
const rootModules = join(dshOutput, 'node_modules');
const hoistedModules = join(rootModules, '.pnpm', 'node_modules');
if (!physicalDependencyLayout) {
  for (const entry of readdirSync(hoistedModules, { withFileTypes: true })) {
    const source = join(hoistedModules, entry.name);
    if (entry.name.startsWith('@') && entry.isDirectory()) {
      for (const scoped of readdirSync(source, { withFileTypes: true })) {
        linkInside(join(rootModules, entry.name, scoped.name), join(source, scoped.name));
      }
    } else {
      linkInside(join(rootModules, entry.name), source);
    }
  }
}

const packages = workspacePackages(repo);
const closure = runtimeWorkspaceClosure(packages, '@deepseek-ai/dsh');
const copiedPackages = join(dshOutput, 'workspace');
for (const name of [...closure].sort()) {
  if (name === '@deepseek-ai/dsh') continue;
  const rootLink = packagePath(rootModules, name);
  if (physicalDependencyLayout) {
    if (lstatExists(rootLink)) continue;
    const entry = packages.get(name);
    if (!entry) fail(`workspace package disappeared while staging: ${name}`);
    copyRuntimePackage(entry.directory, rootLink);
    continue;
  }
  const hoistedLink = packagePath(hoistedModules, name);
  let target;
  if (lstatExists(hoistedLink)) {
    target = hoistedLink;
  } else {
    const entry = packages.get(name);
    if (!entry) fail(`workspace package disappeared while staging: ${name}`);
    target = join(copiedPackages, name.replaceAll('/', '__'));
    copyRuntimePackage(entry.directory, target);
    linkInside(hoistedLink, target);
  }
  linkInside(rootLink, target);
}

for (const link of walkLinks(dshOutput)) {
  const rawTarget = readlinkSync(link);
  const absoluteTarget = resolve(dirname(link), rawTarget);
  const mapping = sourceMappings.find(([source]) => isWithin(absoluteTarget, source));
  if (!mapping) continue;
  const [source, destination] = mapping;
  const rewrittenTarget = join(destination, relative(source, absoluteTarget));
  if (!existsSync(rewrittenTarget)) fail(`cannot internalise link ${link} -> ${absoluteTarget}`);
  rmSync(link, { force: true });
  const target = process.platform === 'win32' ? rewrittenTarget : relative(dirname(link), rewrittenTarget);
  symlinkSync(target, link, lstatSync(rewrittenTarget).isDirectory() ? 'junction' : 'file');
}

for (const link of walkLinks(dshOutput)) {
  const target = resolve(dirname(link), readlinkSync(link));
  if (!isWithin(target, output)) fail(`payload contains an external link: ${link} -> ${target}`);
}

let materializedLinks = 0;
if (physicalDependencyLayout) {
  try {
    materializedLinks = materializeFileLinks(dshOutput);
  } catch (error) {
    fail(error.message);
  }
  const remainingLinks = walkLinks(dshOutput);
  if (remainingLinks.length > 0) fail(`physical payload still contains links: ${remainingLinks.join(', ')}`);
}

const nativePruning = pruneNativePayload(dshOutput, args.platform, args.arch);
let executableRepairs;
try {
  executableRepairs = repairUnixSpawnHelpers(dshOutput, args.platform, args.arch);
} catch (error) {
  fail(error.message);
}

mkdirSync(runtimeBin, { recursive: true });
const nodeDestination = join(runtimeBin, nodeName);
copyFileSync(nodeSource, nodeDestination);
chmodSync(nodeDestination, 0o755);

const runtimeRoot = dirname(dirname(nodeSource));
for (const license of [join(runtimeRoot, 'LICENSE'), join(dirname(nodeSource), 'LICENSE')]) {
  if (existsSync(license)) {
    copyFileSync(license, join(output, 'runtime', 'LICENSE'));
    break;
  }
}

const manifest = JSON.parse(readFileSync(join(repo, 'apps', 'cli', 'package.json'), 'utf8'));
writeFileSync(join(output, 'payload.json'), `${JSON.stringify({
  platform: args.platform,
  arch: args.arch,
  node: capture(nodeSource, ['--version']),
  dsh: manifest.version,
  dshCommit: capture('git', ['-C', repo, 'rev-parse', 'HEAD']),
  deployment: 'pnpm-lockfile',
  dependencyLayout: physicalDependencyLayout ? 'hoisted' : 'isolated',
  materializedLinks,
  executableRepairs,
  nativePruning,
}, null, 2)}\n`);

console.log(
  `stage-payload: ready at ${output}; pruned ${nativePruning.removedDirectories} native directories `
  + `(${nativePruning.removedBytes} bytes)`,
);
