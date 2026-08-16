import { readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

const SUBPROCESS_RUNTIME = join(
  'dsh', 'node_modules', '@deepseek-ai', 'dsh-subprocess-local', 'lib', 'index.js',
);
const HIDDEN_TASKKILL = /spawnSync\("taskkill",\s*\[\s*"\/PID",\s*String\(pid\),\s*"\/T",\s*"\/F"\s*\],\s*\{\s*stdio:\s*"ignore",\s*windowsHide:\s*true\s*\}\);/u;
const VISIBLE_TASKKILL = /spawnSync\("taskkill",\s*\[\s*"\/PID",\s*String\(pid\),\s*"\/T",\s*"\/F"\s*\],\s*\{\s*stdio:\s*"ignore"\s*\}\);/u;
const HIDDEN_SPAWN = /detached:\s*platform\s*!==\s*"win32",\s*windowsHide:\s*platform\s*===\s*"win32"/u;

function replaceOnce(content, pattern, replacement, description) {
  const matches = content.match(new RegExp(pattern.source, pattern.flags.includes('g') ? pattern.flags : `${pattern.flags}g`));
  if (matches?.length !== 1) {
    throw new Error(`cannot apply Windows desktop hardening for ${description}: found ${matches?.length ?? 0} matches`);
  }
  return content.replace(pattern, replacement);
}

/** Make every child-process path owned by the bundled DSH runtime windowless. */
export function hardenWindowsSubprocessRuntime(payloadRoot) {
  const path = join(payloadRoot, SUBPROCESS_RUNTIME);
  let content = readFileSync(path, 'utf8');
  if (!HIDDEN_TASKKILL.test(content)) {
    content = replaceOnce(
      content,
      VISIBLE_TASKKILL,
      'spawnSync("taskkill", ["/PID", String(pid), "/T", "/F"], { stdio: "ignore", windowsHide: true });',
      'taskkill',
    );
  }
  if (!HIDDEN_SPAWN.test(content)) {
    content = replaceOnce(
      content,
      /detached:\s*platform\s*!==\s*"win32",?/u,
      'detached: platform !== "win32",\n\t\twindowsHide: platform === "win32",',
      'spawn',
    );
  }
  writeFileSync(path, content);
  return 2;
}

/** Report missing window-hiding contracts in the bundled DSH runtime. */
export function windowsSubprocessVisibilityViolations(payloadRoot) {
  const path = join(payloadRoot, SUBPROCESS_RUNTIME);
  const content = readFileSync(path, 'utf8');
  const violations = [];
  if (!HIDDEN_TASKKILL.test(content)) {
    violations.push(`${path}: taskkill is not hidden`);
  }
  if (!HIDDEN_SPAWN.test(content)) {
    violations.push(`${path}: spawned subprocesses are not hidden`);
  }
  return violations;
}
