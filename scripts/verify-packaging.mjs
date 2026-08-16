#!/usr/bin/env node

import { readFileSync } from 'node:fs';

function assertFile(path, checks) {
  const content = readFileSync(path, 'utf8');
  for (const [description, pattern] of checks) {
    if (!pattern.test(content)) {
      console.error(`verify-packaging: ${path} does not ${description}`);
      process.exit(1);
    }
  }
}

function rejectFile(path, checks) {
  const content = readFileSync(path, 'utf8');
  for (const [description, pattern] of checks) {
    if (pattern.test(content)) {
      console.error(`verify-packaging: ${path} unexpectedly ${description}`);
      process.exit(1);
    }
  }
}

assertFile('build/darwin/Taskfile.yml', [
  ['copy payload into Contents/Resources before signing', /Contents\/Resources\/payload[\s\S]*codesign/],
]);
assertFile('build/windows/nsis/project.nsi', [
  ['install the payload beside the executable', /SetOutPath "\$INSTDIR\\payload"[\s\S]*File \/r "payload\\\*"/],
]);
assertFile('build/windows/Taskfile.yml', [
  ['provide an installer task that reuses the CI-built executable', /create:nsis:installer:from-build:[\s\S]*common:verify:payload[\s\S]*test -f .*APP_NAME.*\.exe/],
]);
assertFile('scripts/stage-payload.mjs', [
  ['stage a hoisted dependency tree for Windows', /physicalDependencyLayout[\s\S]*node-linker=hoisted/],
  ['use lockfile-based modern pnpm deploy', /inject-workspace-packages=true[\s\S]*deploy', '--prod', '--frozen-lockfile', dshOutput/],
  ['materialize residual file links for Windows', /materializeFileLinks\(dshOutput\)/],
  ['hydrate the Linux node-pty build output', /hydrateLinuxNodePtyBuild\(/],
  ['repair Unix node-pty spawn-helper modes', /repairUnixSpawnHelpers\(dshOutput/],
]);
assertFile('scripts/verify-payload.mjs', [
  ['reject links in Windows payloads', /args\.platform === 'windows'[\s\S]*Windows payload contains a link/],
  ['verify the target of the bundled Node executable', /executableTarget\(nodePath\)/],
]);
assertFile('build/Taskfile.yml', [
  ['scope binding fingerprints to the root Go package', /generate:bindings:[\s\S]*sources:\s*\n\s*- "\*\.go"\s*\n\s*- go\.mod\s*\n\s*- go\.sum/],
]);
rejectFile('build/windows/Taskfile.yml', [
  ['runs go mod tidy while the physical Windows payload is staged', /build:native:[\s\S]*common:go:mod:tidy/],
]);
assertFile('build/linux/nfpm/nfpm.yaml', [
  ['use an explicit architecture template', /arch: "__GOARCH__"/],
  ['install the target payload tree beside the executable', /src: "\.\/build\/payload\/linux-__GOARCH__\/"[\s\S]*dst: "\/usr\/local\/bin\/payload"[\s\S]*type: "tree"/],
]);
assertFile('build/linux/Taskfile.yml', [
  ['repack AppImage after adding payload', /repack-appimage-payload\.sh/],
  ['render the nFPM config for the target architecture', /package-linux\.mjs --format deb --arch/],
]);
assertFile('.github/workflows/build.yml', [
  ['park the physical Windows payload while Wails builds', /parked_payload=.*windows-\$\{GOARCH\}-payload[\s\S]*trap restore_windows_payload EXIT[\s\S]*wails3 task windows:build[\s\S]*restore_windows_payload/],
  ['assemble NSIS without rebuilding over the staged payload', /windows:create:nsis:installer:from-build ARCH=\$GOARCH/],
  ['smoke-test the installed NSIS payload', /Verify installed NSIS payload[\s\S]*smoke-payload\.mjs[\s\S]*--server/],
  ['extract and smoke-test the AppImage payload', /Verify AppImage payload[\s\S]*--appimage-extract[\s\S]*smoke-payload\.mjs[\s\S]*--server/],
]);
assertFile('frontend/dist/index.html', [
  ['use the Wails 3 runtime module', /import \{ Events \} from "\/wails\/runtime\.js"/],
  ['subscribe before the splash-ready handshake', /Events\.On\('dsh:error'[\s\S]*Events\.Emit\('dsh:splash-ready'\)/],
]);

console.log('verify-packaging: packaging and splash contracts are present');
