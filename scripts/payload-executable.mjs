import {
  closeSync,
  existsSync,
  openSync,
  readSync,
  realpathSync,
  writeSync,
} from 'node:fs';
import { resolve } from 'node:path';

const ARCHES = {
  0x01000007: 'amd64', // Mach-O CPU_TYPE_X86_64
  0x0100000c: 'arm64', // Mach-O CPU_TYPE_ARM64
};

function readHeader(path) {
  const buffer = Buffer.alloc(4096);
  const fd = openSync(path, 'r');
  try {
    const length = readSync(fd, buffer, 0, buffer.length, 0);
    return buffer.subarray(0, length);
  } finally {
    closeSync(fd);
  }
}

function windowsPEHeader(path) {
  const header = readHeader(path);
  if (header.length < 64 || header.subarray(0, 2).toString('ascii') !== 'MZ') return undefined;
  const peOffset = header.readUInt32LE(0x3c);
  if (peOffset + 24 > header.length
    || !header.subarray(peOffset, peOffset + 4).equals(Buffer.from('PE\0\0'))) return undefined;
  const optionalSize = header.readUInt16LE(peOffset + 20);
  const optionalOffset = peOffset + 24;
  const subsystemOffset = optionalOffset + 68;
  if (optionalSize < 70 || subsystemOffset + 2 > header.length) return undefined;
  const magic = header.readUInt16LE(optionalOffset);
  if (magic !== 0x10b && magic !== 0x20b) return undefined;
  return { header, peOffset, subsystemOffset };
}

export function resolveExecutablePath(path, platform = process.platform) {
  const requested = resolve(path);
  const candidates = platform === 'win32' && !requested.toLowerCase().endsWith('.exe')
    ? [requested, `${requested}.exe`]
    : [requested];
  const executable = candidates.find(candidate => existsSync(candidate));
  if (!executable) throw new Error(`executable does not exist: ${requested}`);
  return realpathSync(executable);
}

export function executableTarget(path) {
  const header = readHeader(path);

  if (header.length >= 20
    && header[0] === 0x7f && header.subarray(1, 4).toString('ascii') === 'ELF') {
    const littleEndian = header[5] === 1;
    const machine = littleEndian ? header.readUInt16LE(18) : header.readUInt16BE(18);
    const arch = machine === 0x3e ? 'amd64' : machine === 0xb7 ? 'arm64' : undefined;
    return arch ? { platform: 'linux', arch } : undefined;
  }

  if (header.length >= 12 && header.readUInt32LE(0) === 0xfeedfacf) {
    const arch = ARCHES[header.readUInt32LE(4)];
    return arch ? { platform: 'darwin', arch } : undefined;
  }

  if (header.length >= 64 && header.subarray(0, 2).toString('ascii') === 'MZ') {
    const peOffset = header.readUInt32LE(0x3c);
    if (peOffset + 6 <= header.length
      && header.subarray(peOffset, peOffset + 4).equals(Buffer.from('PE\0\0'))) {
      const machine = header.readUInt16LE(peOffset + 4);
      const arch = machine === 0x8664 ? 'amd64' : machine === 0xaa64 ? 'arm64' : undefined;
      return arch ? { platform: 'windows', arch } : undefined;
    }
  }

  return undefined;
}

/** Return the PE subsystem value, or undefined when the file is not a valid PE image. */
export function windowsPESubsystem(path) {
  const pe = windowsPEHeader(path);
  return pe?.header.readUInt16LE(pe.subsystemOffset);
}

/**
 * Mark a Windows runtime as a GUI process so Windows cannot allocate a visible
 * console for it. Explicit stdout/stderr pipes continue to work normally.
 */
export function setWindowsGUISubsystem(path) {
  const pe = windowsPEHeader(path);
  if (!pe) throw new Error(`cannot locate a valid PE optional header in ${path}`);
  const value = Buffer.alloc(2);
  value.writeUInt16LE(2, 0); // IMAGE_SUBSYSTEM_WINDOWS_GUI
  const fd = openSync(path, 'r+');
  try {
    writeSync(fd, value, 0, value.length, pe.subsystemOffset);
  } finally {
    closeSync(fd);
  }
}
