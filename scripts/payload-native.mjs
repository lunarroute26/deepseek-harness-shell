import {
  existsSync,
  chmodSync,
  lstatSync,
  readdirSync,
  rmSync,
  statSync,
} from 'node:fs';
import { basename, join } from 'node:path';

const PLATFORM_NAMES = {
  darwin: 'darwin',
  linux: 'linux',
  windows: 'win32',
};

const ARCH_NAMES = {
  '386': 'ia32',
  amd64: 'x64',
  arm64: 'arm64',
  ia32: 'ia32',
  x64: 'x64',
};

function targetNames(platform, arch) {
  const nodePlatform = PLATFORM_NAMES[platform];
  const nodeArch = ARCH_NAMES[arch];
  if (!nodePlatform) throw new Error(`unsupported payload platform: ${platform}`);
  if (!nodeArch) throw new Error(`unsupported payload architecture: ${arch}`);
  return { nodePlatform, nodeArch };
}

function findNodePtyDirectories(root) {
  if (!existsSync(root)) return [];
  const packages = [];
  const visit = directory => {
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      if (!entry.isDirectory()) continue;
      const path = join(directory, entry.name);
      if (entry.name === 'node-pty' && basename(directory) === 'node_modules') {
        packages.push(path);
      } else {
        visit(path);
      }
    }
  };
  visit(root);
  return packages;
}

function directorySize(path) {
  const stat = lstatSync(path);
  if (!stat.isDirectory() || stat.isSymbolicLink()) return stat.size;
  return readdirSync(path).reduce((total, entry) => total + directorySize(join(path, entry)), 0);
}

function removeDirectory(path, result) {
  result.removedBytes += directorySize(path);
  result.removedDirectories += 1;
  rmSync(path, { recursive: true, force: true });
}

function prebuildDirectories(packageRoot) {
  const root = join(packageRoot, 'prebuilds');
  if (!existsSync(root)) return [];
  return readdirSync(root, { withFileTypes: true })
    .filter(entry => entry.isDirectory())
    .map(entry => join(root, entry.name));
}

function conptyArchitectureDirectories(packageRoot) {
  const root = join(packageRoot, 'third_party', 'conpty');
  if (!existsSync(root)) return [];
  const paths = [];
  for (const version of readdirSync(root, { withFileTypes: true })) {
    if (!version.isDirectory()) continue;
    const versionRoot = join(root, version.name);
    for (const entry of readdirSync(versionRoot, { withFileTypes: true })) {
      if (entry.isDirectory() && /^win10-(?:arm64|x64|ia32)$/.test(entry.name)) {
        paths.push(join(versionRoot, entry.name));
      }
    }
  }
  return paths;
}

export function unexpectedNativeDirectories(root, platform, arch) {
  const { nodePlatform, nodeArch } = targetNames(platform, arch);
  const expectedPrebuild = `${nodePlatform}-${nodeArch}`;
  const expectedConpty = `win10-${nodeArch}`;
  const unexpected = [];

  for (const packageRoot of findNodePtyDirectories(root)) {
    for (const path of prebuildDirectories(packageRoot)) {
      if (basename(path) !== expectedPrebuild) unexpected.push(path);
    }
    for (const path of conptyArchitectureDirectories(packageRoot)) {
      if (platform !== 'windows' || basename(path) !== expectedConpty) unexpected.push(path);
    }
  }
  return unexpected;
}

export function pruneNativePayload(root, platform, arch) {
  // node-pty publishes every macOS and Windows prebuild in one package. Its
  // loader selects exactly process.platform-process.arch, so all other
  // directories are build-time baggage and must not enter an installer.
  const result = { removedDirectories: 0, removedBytes: 0 };
  for (const path of unexpectedNativeDirectories(root, platform, arch)) {
    removeDirectory(path, result);
  }
  return result;
}

function expectedUnixSpawnHelper(packageRoot, platform, arch) {
  const { nodePlatform, nodeArch } = targetNames(platform, arch);
  if (platform === 'darwin') {
    return join(packageRoot, 'prebuilds', `${nodePlatform}-${nodeArch}`, 'spawn-helper');
  }
  if (platform === 'linux') return join(packageRoot, 'build', 'Release', 'spawn-helper');
  return undefined;
}

export function invalidUnixSpawnHelpers(root, platform, arch) {
  if (platform === 'windows') return [];
  return findNodePtyDirectories(root)
    .map(packageRoot => expectedUnixSpawnHelper(packageRoot, platform, arch))
    .filter(path => !existsSync(path) || (statSync(path).mode & 0o111) === 0);
}

export function repairUnixSpawnHelpers(root, platform, arch) {
  if (platform === 'windows') return 0;
  const packageRoots = findNodePtyDirectories(root);
  const helpers = packageRoots.map(packageRoot => expectedUnixSpawnHelper(packageRoot, platform, arch));
  const missing = helpers.filter(path => !existsSync(path));
  if (missing.length > 0) {
    throw new Error(`node-pty spawn-helper is missing: ${missing.join(', ')}`);
  }
  for (const helper of helpers) chmodSync(helper, 0o755);
  return helpers.length;
}
