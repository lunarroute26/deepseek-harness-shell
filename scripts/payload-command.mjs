export function commandInvocation(command, args, {
  platform = process.platform,
  comspec = process.env.ComSpec,
} = {}) {
  if (platform === 'win32' && command === 'pnpm') {
    return {
      executable: comspec || 'cmd.exe',
      args: ['/d', '/s', '/c', 'pnpm.cmd', ...args],
    };
  }
  return { executable: command, args };
}
