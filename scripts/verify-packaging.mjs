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

function assertContains(path, description, expected) {
  const content = readFileSync(path, 'utf8');
  if (!content.includes(expected)) {
    console.error(`verify-packaging: ${path} does not ${description}`);
    process.exit(1);
  }
}

const taskfile = readFileSync('Taskfile.yml', 'utf8');
const appVersion = taskfile.match(/APP_VERSION: "([^"]+)"/)?.[1];
if (!appVersion) {
  console.error('verify-packaging: Taskfile.yml does not define APP_VERSION');
  process.exit(1);
}
for (const [path, expected] of [
  ['main.go', `var version = "${appVersion}-dev"`],
  ['build/config.yml', `version: "${appVersion}"`],
  ['build/windows/wails.exe.manifest', `version="${appVersion}"`],
  ['build/windows/info.json', `"ProductVersion": "${appVersion}"`],
  ['build/windows/nsis/project.nsi', `!define INFO_PRODUCTVERSION "${appVersion}"`],
  ['build/windows/nsis/wails_tools.nsh', `!define INFO_PRODUCTVERSION "${appVersion}"`],
  ['build/windows/msix/app_manifest.xml', `Version="${appVersion}.0"`],
  ['build/windows/msix/template.xml', `Version="${appVersion}.0"`],
  ['build/darwin/Info.plist', `<string>${appVersion}</string>`],
  ['build/darwin/Info.dev.plist', `<string>${appVersion}</string>`],
  ['build/linux/nfpm/nfpm.yaml', `version: "${appVersion}"`],
  ['build/ios/Info.plist', `<string>${appVersion}</string>`],
  ['build/ios/Info.dev.plist', `<string>${appVersion}-dev</string>`],
  ['.github/workflows/build.yml', `VERSION="${appVersion}-dev"`],
]) {
  assertContains(path, `use application version ${appVersion}`, expected);
}

assertFile('main.go', [
  ['set the application name to deepseek harness shell', /Name:\s+"deepseek harness shell"/],
  ['set the window title to deepseek harness shell', /Title:\s+"deepseek harness shell"/],
  ['embed the canonical application icon', /\/\/go:embed build\/appicon\.png[\s\S]*var appIcon \[\]byte/],
  ['set the runtime application icon', /Icon:\s+appIcon/],
  ['replace the default Wails application menu', /app\.Menu\.Set\(newApplicationMenu\(app\)\)/],
  ['place manual update checking directly below About', /appMenu\.AddRole\(application\.About\)[\s\S]*appMenu\.Add\("Check for Updates\.\.\."\)[\s\S]*appMenu\.AddSeparator\(\)[\s\S]*appMenu\.AddRole\(application\.ServicesMenu\)/],
  ['run the manual check through the built-in updater window', /func checkForUpdates\([\s\S]*app\.Updater\.CheckAndInstall\(ctx\)/],
]);
rejectFile('main.go', [
  ['includes the Wails help menu', /application\.HelpMenu/],
]);

assertFile('build/config.yml', [
  ['define the canonical product name', /productName: "deepseek harness shell"/],
  ['define the canonical copyright', /copyright: "\(c\) 2026 deepseek harness shell"/],
]);
assertFile('build/windows/info.json', [
  ['define the canonical Windows product name', /"ProductName": "deepseek harness shell"/],
  ['define the canonical Windows copyright', /"LegalCopyright": "\(c\) 2026 deepseek harness shell"/],
]);
assertFile('build/windows/nsis/project.nsi', [
  ['define the canonical NSIS product name', /!define INFO_PRODUCTNAME\s+"deepseek harness shell"/],
  ['define the canonical NSIS copyright', /!define INFO_COPYRIGHT\s+"\(c\) 2026 deepseek harness shell"/],
]);
assertFile('build/darwin/Info.plist', [
  ['define the canonical macOS product name', /<string>deepseek harness shell<\/string>/],
  ['define the canonical macOS copyright', /<string>\(c\) 2026 deepseek harness shell<\/string>/],
]);
assertFile('build/darwin/Info.dev.plist', [
  ['use a distinct development bundle identifier', /<string>com\.deepseek\.harness\.dev<\/string>/],
]);
assertFile('build/darwin/Taskfile.yml', [
  ['use the application icon for the DMG file', /DMG_FILE_ICON:.*build\/darwin\/icons\.icns/],
  ['copy the same icon into production and development bundles', /create:app:bundle:[\s\S]*cp build\/darwin\/icons\.icns[\s\S]*run:[\s\S]*cp build\/darwin\/icons\.icns/],
]);
assertFile('Taskfile.yml', [
  ['separate the product display name from the binary name', /PRODUCT_NAME: "deepseek harness shell"/],
]);
assertFile('build/linux/Taskfile.yml', [
  ['generate the desktop entry with the canonical display name', /generate \.desktop -name "\{\{\.PRODUCT_NAME\}\}"/],
]);

for (const metadataFile of [
  'main.go',
  'app_logger.go',
  'frontend/dist/index.html',
  'build/config.yml',
  'build/windows/info.json',
  'build/windows/nsis/project.nsi',
  'build/windows/nsis/wails_tools.nsh',
  'build/windows/wails.exe.manifest',
  'build/windows/msix/app_manifest.xml',
  'build/windows/msix/template.xml',
  'build/darwin/Info.plist',
  'build/darwin/Info.dev.plist',
  'build/linux/desktop',
  'build/linux/nfpm/nfpm.yaml',
  'build/ios/Info.plist',
  'build/ios/Info.dev.plist',
  'build/ios/LaunchScreen.storyboard',
  'build/ios/project.pbxproj',
  'build/android/app/src/main/res/values/strings.xml',
  'build/android/settings.gradle',
  'LICENSE',
]) {
  rejectFile(metadataFile, [
    ['contains obsolete product metadata', /DeepSeek Harness|My Product|My Company|wailsref|Wails App\b/],
  ]);
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
for (const taskfile of [
  'build/darwin/Taskfile.yml',
  'build/linux/Taskfile.yml',
  'build/windows/Taskfile.yml',
]) {
  rejectFile(taskfile, [
    ['builds the static desktop frontend or regenerates unused Wails bindings', /common:build:frontend/],
  ]);
}
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
  ['build Windows directly while the physical payload is parked', /parked_payload=.*windows-\$\{GOARCH\}-payload[\s\S]*trap restore_windows_payload EXIT[\s\S]*wails3 generate syso[\s\S]*CGO_ENABLED=0 go build[\s\S]*restore_windows_payload/],
  ['assemble NSIS without rebuilding over the staged payload', /windows:create:nsis:installer:from-build ARCH=\$GOARCH/],
  ['smoke-test the installed NSIS payload', /Verify installed NSIS payload[\s\S]*smoke-payload\.mjs[\s\S]*--server/],
  ['extract and smoke-test the AppImage payload', /Verify AppImage payload[\s\S]*--appimage-extract[\s\S]*smoke-payload\.mjs[\s\S]*--server/],
]);
rejectFile('.github/workflows/build.yml', [
  ['runs the Wails Task executor for the Windows application build', /wails3 task windows:build/],
]);
assertFile('frontend/dist/index.html', [
  ['use the Wails 3 runtime module', /import \{ Events \} from "\/wails\/runtime\.js"/],
  ['subscribe before the splash-ready handshake', /Events\.On\('dsh:error'[\s\S]*Events\.Emit\('dsh:splash-ready'\)/],
]);

console.log('verify-packaging: packaging and splash contracts are present');
