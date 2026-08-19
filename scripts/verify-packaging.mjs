#!/usr/bin/env node

import { createHash } from 'node:crypto';
import { existsSync, readFileSync } from 'node:fs';

function fail(message) {
  console.error(`verify-packaging: ${message}`);
  process.exit(1);
}

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

function assertAbsent(path) {
  if (existsSync(path)) {
    fail(`obsolete branding asset still exists: ${path}`);
  }
}

function assertFilesEqual(reference, candidate) {
  if (!readFileSync(reference).equals(readFileSync(candidate))) {
    fail(`${candidate} does not match canonical asset ${reference}`);
  }
}

function assertPNGDimensions(path, expectedWidth, expectedHeight) {
  const content = readFileSync(path);
  if (content.subarray(0, 8).toString('hex') !== '89504e470d0a1a0a') {
    fail(`${path} is not a PNG image`);
  }
  const width = content.readUInt32BE(16);
  const height = content.readUInt32BE(20);
  if (width !== expectedWidth || height !== expectedHeight) {
    fail(`${path} is ${width}x${height}, expected ${expectedWidth}x${expectedHeight}`);
  }
}

function sha256(path) {
  return createHash('sha256').update(readFileSync(path)).digest('hex');
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
  ['set the application name from the canonical name', /Name:\s+applicationName/],
  ['set the window title from the canonical name', /Title:\s+applicationName/],
  ['embed the canonical application icon', /\/\/go:embed build\/appicon\.png[\s\S]*var appIcon \[\]byte/],
  ['embed the tray icon derived from the canonical icon', /\/\/go:embed build\/trayicon\.png[\s\S]*var trayIcon \[\]byte/],
  ['set the runtime application icon', /Icon:\s+appIcon/],
  ['replace the default Wails application menu', /app\.Menu\.Set\(newApplicationMenu\(app\)\)/],
  ['place manual update checking directly below About', /appMenu\.AddRole\(application\.About\)[\s\S]*appMenu\.Add\("Check for Updates\.\.\."\)[\s\S]*appMenu\.AddSeparator\(\)[\s\S]*appMenu\.AddRole\(application\.ServicesMenu\)/],
  ['run the manual check through the built-in updater window', /func checkForUpdates\([\s\S]*app\.Updater\.CheckAndInstall\(context\.Background\(\)\)/],
  ['avoid a whole-body HTTP client deadline', /httpClient\s*:=\s*newUpdaterHTTPClient\(\)[\s\S]*HTTPClient:\s+httpClient/],
  ['wrap GitHub downloads with resume support', /newResumableGitHubProvider\([\s\S]*updateDownloadIdleTimeout/],
  ['start dsh once when the native window runtime is ready', /window\.OnWindowEvent\(events\.Common\.WindowRuntimeReady[\s\S]*activateOnce\.Do\(lifecycle\.SplashReady\)/],
  ['keep the app alive after its last window closes on macOS', /ApplicationShouldTerminateAfterLastWindowClosed:\s+false/],
  ['keep the app alive after its last window closes on Windows', /Windows:\s+application\.WindowsOptions\{[\s\S]*DisableQuitOnLastWindowClosed:\s+true/],
  ['keep the app alive after its last window closes on Linux', /Linux:\s+application\.LinuxOptions\{[\s\S]*DisableQuitOnLastWindowClosed:\s+true/],
  ['enforce a single desktop application instance', /SingleInstance:\s+&application\.SingleInstanceOptions\{[\s\S]*UniqueID:\s+"com\.deepseek\.harness"[\s\S]*OnSecondInstanceLaunch:/],
  ['route raw WebView messages through the shell download manager', /RawMessageHandler:[\s\S]*downloads\.handleRawMessage/],
  ['bind the download bridge to the active dynamic dsh URL', /downloads\.setDSHBaseURL\(msg\)/],
]);
rejectFile('main.go', [
  ['includes the Wails help menu', /application\.HelpMenu/],
  ['exposes a non-tray quit menu role', /AddRole\(application\.Quit\)/],
  ['sets an http.Client timeout across the complete update body', /HTTPClient:\s+&http\.Client\{Timeout:/],
]);
assertFile('tray.go', [
  ['define the canonical application name', /const applicationName = "deepseek harness shell"/],
  ['intercept close synchronously and hide the main window', /RegisterHook\(events\.Common\.WindowClosing[\s\S]*window\.Hide\(\)[\s\S]*event\.Cancel\(\)/],
  ['restore the main window from the tray', /menu\.Add\("打开主界面"\)[\s\S]*tray\.OnClick\(controller\.showMainWindow\)/],
  ['make the tray menu the explicit application exit path', /menu\.Add\("退出 " \+ applicationName\)[\s\S]*controller\.requestExit\(\)/],
  ['open the tray menu on right click', /tray\.OnRightClick\(tray\.OpenMenu\)/],
  ['use a macOS template icon and regular icons elsewhere', /runtime\.GOOS == "darwin"[\s\S]*tray\.SetTemplateIcon\(icon\)[\s\S]*tray\.SetIcon\(icon\)/],
  ['restore the window when the macOS Dock icon is clicked', /events\.Mac\.ApplicationShouldHandleReopen[\s\S]*controller\.showMainWindow\(\)/],
]);
rejectFile('tray.go', [
  ['attaches the main window as a tray popup', /AttachWindow\(/],
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
  ['keep per-file installation details hidden', /ShowInstDetails nevershow[\s\S]*SetDetailsPrint none[\s\S]*File \/r "payload\\\*"[\s\S]*SetDetailsPrint both/],
]);
rejectFile('build/windows/nsis/project.nsi', [
  ['forces the installer to wait on the file-details page', /MUI_FINISHPAGE_NOAUTOCLOSE/],
]);
rejectFile('build/windows/nsis/wails_tools.nsh', [
  ['rescans the complete install tree to estimate its size', /\$\{GetSize\}\s+"\$INSTDIR"/],
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
  ['recreate production and development bundles before assembling them', /create:app:bundle:[\s\S]*rm -rf "\{\{\.BIN_DIR\}\}\/\{\{\.APP_NAME\}\}\.app"[\s\S]*run:[\s\S]*rm -rf "\{\{\.BIN_DIR\}\}\/\{\{\.APP_NAME\}\}\.dev\.app"/],
  ['copy the same icon into production and development bundles', /create:app:bundle:[\s\S]*cp build\/darwin\/icons\.icns[\s\S]*run:[\s\S]*cp build\/darwin\/icons\.icns/],
]);
rejectFile('build/darwin/Taskfile.yml', [
  ['copies a competing macOS asset catalogue', /Assets\.car/],
]);
assertFile('build/Taskfile.yml', [
  ['generate macOS and Windows icons only from appicon.png', /wails3 generate icons -input appicon\.png -macfilename darwin\/icons\.icns -windowsfilename windows\/icon\.ico/],
]);
rejectFile('build/Taskfile.yml', [
  ['generates a competing Icon Composer asset', /iconcomposerinput|macassetdir|appicon\.icon/],
]);
assertFile('build/appicon.svg', [
  ['use the project fish logo in the canonical brand colour', /viewBox="0 0 50 50"[\s\S]*49\.3315[\s\S]*fill="#4D6BFE"/],
]);
assertFile('frontend/dist/index.html', [
  ['show the canonical icon on the splash screen', /<img src="\/appicon\.png" width="88" height="88" alt="" \/>/],
]);
assertFile('download_bridge.go', [
  ['inject the bridge after navigation on all desktop platforms', /WebViewDidFinishNavigation[\s\S]*WebViewNavigationCompleted[\s\S]*WindowLoadFinished/],
  ['validate the message origin against the active dsh origin', /downloadMessageOriginAllowed\(originInfo, baseURL\)/],
]);
assertFile('download.go', [
  ['allow only the session export endpoint', /parsed\.Path != "\/api\/session\.export"/],
  ['stream downloads into a temporary part file', /os\.CreateTemp\([\s\S]*\.part/],
  ['publish downloads only after replacing the destination', /replaceDownloadedFile\(temporaryPath, destination\)/],
]);
assertFile('frontend/download-bridge.js', [
  ['declare shell download messages without modifying upstream source', /dsh-shell:download-request/],
  ['intercept detached anchor clicks', /HTMLAnchorElement\.prototype\.click/],
  ['limit interception to the session export endpoint', /url\.pathname !== '\/api\/session\.export'/],
]);
assertFile('frontend/dist/download.html', [
  ['load the shell-owned download task UI', /id="tasks"[\s\S]*transfer-window\.js/],
]);
assertFile('frontend/dist/transfer-window.js', [
  ['support cancelling and revealing shell downloads', /'cancel'[\s\S]*'reveal'/],
]);

for (const obsoletePath of [
  'build/appicon.icon',
  'build/darwin/Assets.car',
  'build/darwin/dmg-file-icon.icns',
  'build/darwin/dmg-file-icon.png',
]) {
  assertAbsent(obsoletePath);
}

assertPNGDimensions('build/appicon.png', 1024, 1024);
assertPNGDimensions('build/trayicon.png', 64, 64);
assertPNGDimensions('frontend/dist/appicon.png', 1024, 1024);
assertPNGDimensions('build/ios/icon.png', 1024, 1024);
assertPNGDimensions('build/darwin/dmg-background.png', 540, 380);
assertFilesEqual('build/appicon.png', 'frontend/dist/appicon.png');
assertFilesEqual('build/appicon.png', 'build/ios/icon.png');

const androidIconSizes = new Map([
  ['mdpi', 48],
  ['hdpi', 72],
  ['xhdpi', 96],
  ['xxhdpi', 144],
  ['xxxhdpi', 192],
]);
for (const [density, size] of androidIconSizes) {
  const launcher = `build/android/app/src/main/res/mipmap-${density}/ic_launcher.png`;
  const roundLauncher = `build/android/app/src/main/res/mipmap-${density}/ic_launcher_round.png`;
  assertPNGDimensions(launcher, size, size);
  assertFilesEqual(launcher, roundLauncher);
}

const obsoleteBrandingHashes = new Set([
  '6092cfceffdd897ba731cc567ffd1b7eb9319bae74a6aed6853e5df38d01d7ac',
  'b0080a69ad4baffc146d1a43a839bd0a8f33694d5fbbee9342edb113f822e738',
  '3fe3fd7fcb86fd233f74bab3a0a3ddb160d795ca66aa744bab87f67d830252d1',
  '5f07c1d4eafd111b76316b7d25ab50906848cf468d6862b1b101c20a89a920df',
  '978e228fc11030aa8b350a166dfc39e2e541c0691743d1b66aacbc02b2f8c6a6',
  'ada25c1f67429135153a7ecc84c8e2381f615645d7442d08744a0464d13eca3e',
  '5e5b110d28b8f438d33f4e3aeb194b2ff4d68afb56d419591f7bfd54a6e90291',
  '3276f8cc8c1ad6f78f10eb38bed8a6263d201a464e78d1640799dd10183aaf30',
  '1fcf58094b85c589a67a5e7972e26f08ce721971bdfd43b7c51e84ec412c923f',
]);
for (const asset of [
  'build/appicon.png',
  'build/darwin/icons.icns',
  'build/windows/icon.ico',
  'build/darwin/dmg-background.png',
  ...[...androidIconSizes.keys()].map(
    (density) => `build/android/app/src/main/res/mipmap-${density}/ic_launcher.png`,
  ),
]) {
  if (obsoleteBrandingHashes.has(sha256(asset))) {
    fail(`${asset} still contains obsolete branding`);
  }
}
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
  ['select target optional dependencies during cross-platform staging', /'--os', nodePlatform[\s\S]*'--cpu', nodeArch/],
  ['accept an explicit target Node version when the binary cannot run on the host', /detectedNodeVersion \|\| args\['node-version'\][\s\S]*cannot execute target Node/],
  ['skip host lifecycle scripts only while cross-platform staging', /crossStaging = process\.platform !== nodePlatform[\s\S]*crossStaging \? \['--ignore-scripts'\]/],
  ['make the bundled Windows Node runtime a GUI process', /setWindowsGUISubsystem\(nodeDestination\)/],
  ['hide subprocesses created by the bundled DSH runtime', /hardenWindowsSubprocessRuntime\(output\)/],
]);
assertFile('scripts/verify-payload.mjs', [
  ['reject links in Windows payloads', /args\.platform === 'windows'[\s\S]*Windows payload contains a link/],
  ['verify the target of the bundled Node executable', /executableTarget\(nodePath\)/],
  ['reject a Windows Node console executable', /windowsPESubsystem\(nodePath\) !== 2/],
  ['verify hidden Windows subprocess contracts', /windowsSubprocessVisibilityViolations\(root\)/],
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
  ['launch the installed Windows GUI and verify its server', /Verify installed NSIS payload[\s\S]*\$appProcess = Start-Process[\s\S]*-FilePath "\$installDir\/deepseek-harness-shell\.exe" -PassThru[\s\S]*Invoke-WebRequest[\s\S]*msg="dsh ready"/],
  ['reproduce and verify migration of legacy Windows profile modules', /Verify installed NSIS payload[\s\S]*dsh-session-query-sqlite[\s\S]*migration-marker\.txt[\s\S]*\.dsh\/backups[\s\S]*legacy profile fallback was not preserved/],
  ['extract and smoke-test the AppImage payload', /Verify AppImage payload[\s\S]*--appimage-extract[\s\S]*smoke-payload\.mjs[\s\S]*--server/],
  ['name the macOS DMG so legacy updaters skip the installer', /mv deepseek-harness-shell\.dmg "\$\{\{ matrix\.artifact \}\}-installer\.dmg"/],
  ['publish the full macOS app as the updater archive', /deepseek-harness-shell\.app "\$\{\{ matrix\.artifact \}\}\.zip"/],
  ['remove release assets that are absent from the rebuilt distribution', /gh release delete-asset "\$GITHUB_REF_NAME" "\$asset" --yes/],
]);
rejectFile('.github/workflows/build.yml', [
  ['runs the Wails Task executor for the Windows application build', /wails3 task windows:build/],
]);
assertFile('frontend/dist/index.html', [
  ['use the Wails 3 runtime module', /import \{ Events \} from "\/wails\/runtime\.js"/],
  ['subscribe before the splash-ready handshake', /Events\.On\('dsh:error'[\s\S]*Events\.Emit\('dsh:splash-ready'\)/],
]);

console.log('verify-packaging: packaging and splash contracts are present');
