import { closeSync, openSync, readSync } from 'node:fs';

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
