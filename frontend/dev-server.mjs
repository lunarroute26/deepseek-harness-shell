#!/usr/bin/env node
// Zero-dependency static dev server for Wails v3 dev mode.
// Serves frontend/dist so `wails3 dev` (which sets FRONTEND_DEVSERVER_URL
// and proxies through it) can load the prebuilt splash page.
// The real dsh UI is served later by the spawned node process.
//
// 监听说明：Wails 用 http://localhost:9245 探测，macOS 上 localhost 常先解析为 ::1。
// 因此这里监听 '::'（双栈，ipv6Only=false），同时接受 ::1 与 127.0.0.1。
import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { extname, join, normalize, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

// 去掉尾部斜杠，避免后续 startsWith(root + sep) 双斜杠误判越界。
const rootDir = normalize(fileURLToPath(new URL('./dist', import.meta.url)));

// Port resolution: CLI arg (--port N) > WAILS_VITE_PORT > 9245
const argv = process.argv.slice(2);
let port = Number(process.env.WAILS_VITE_PORT) || 9245;
for (let i = 0; i < argv.length - 1; i++) {
  if (argv[i] === '--port') {
    const n = Number(argv[i + 1]);
    if (Number.isInteger(n) && n > 0) port = n;
  }
}

const MIME = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.mjs': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.ico': 'image/x-icon',
  '.woff': 'font/woff',
  '.woff2': 'font/woff2',
};

const server = createServer(async (req, res) => {
  try {
    const url = new URL(req.url, 'http://127.0.0.1');
    let pathname = decodeURIComponent(url.pathname);
    if (pathname === '' || pathname === '/') pathname = '/index.html';
    const file = normalize(join(rootDir, pathname));
    // 防目录穿越：解析后必须仍在 rootDir 之内
    if (file !== rootDir && !file.startsWith(rootDir + sep)) {
      res.writeHead(403, { 'content-type': 'text/plain' });
      res.end('forbidden');
      return;
    }
    let body;
    try {
      body = await readFile(file);
    } catch {
      body = await readFile(join(rootDir, 'index.html')); // SPA fallback
    }
    res.writeHead(200, { 'content-type': MIME[extname(file)] || 'application/octet-stream' });
    res.end(body);
  } catch (err) {
    res.writeHead(500, { 'content-type': 'text/plain' });
    res.end(String(err));
  }
});

// '::' 双栈监听：IPv6 优先，同时接受 IPv4-mapped 的 127.0.0.1
server.listen(port, '::', () => {
  console.log(`[shell] static dev server: http://localhost:${port} (serving ${rootDir})`);
});
