#!/usr/bin/env node

import {
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const ARCH_PLACEHOLDER = '__GOARCH__';
const FORMATS = new Set(['deb', 'rpm', 'archlinux']);

export function renderNfpmConfig(template, arch) {
  if (!/^[a-z0-9_]+$/.test(arch)) {
    throw new Error(`invalid Linux architecture: ${arch}`);
  }
  if (!template.includes(ARCH_PLACEHOLDER)) {
    throw new Error(`nFPM template is missing ${ARCH_PLACEHOLDER}`);
  }

  return template.replaceAll(ARCH_PLACEHOLDER, arch);
}

function parseArgs(argv) {
  const args = {};
  for (let index = 0; index < argv.length; index += 2) {
    const key = argv[index];
    const value = argv[index + 1];
    if (!key?.startsWith('--') || value === undefined) {
      throw new Error(`invalid argument near ${key ?? '<end>'}`);
    }
    args[key.slice(2)] = value;
  }
  return args;
}

function main() {
  const args = parseArgs(process.argv.slice(2));
  if (!FORMATS.has(args.format)) {
    throw new Error(`unsupported Linux package format: ${args.format ?? '<missing>'}`);
  }
  if (!args.arch || !args.out) {
    throw new Error('--arch and --out are required');
  }

  const source = args.config ?? 'build/linux/nfpm/nfpm.yaml';
  const temporaryDirectory = mkdtempSync(join(tmpdir(), 'dsh-nfpm-'));
  const renderedConfig = join(temporaryDirectory, 'nfpm.yaml');

  try {
    writeFileSync(
      renderedConfig,
      renderNfpmConfig(readFileSync(source, 'utf8'), args.arch),
    );
    const result = spawnSync(
      'wails3',
      [
        'tool', 'package',
        '-name', 'deepseek-harness-shell',
        '-format', args.format,
        '-config', renderedConfig,
        '-out', args.out,
      ],
      { stdio: 'inherit' },
    );
    if (result.error) {
      throw result.error;
    }
    if (result.status !== 0) {
      process.exitCode = result.status ?? 1;
    }
  } finally {
    rmSync(temporaryDirectory, { recursive: true, force: true });
  }
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  try {
    main();
  } catch (error) {
    console.error(`package-linux: ${error.message}`);
    process.exitCode = 1;
  }
}
