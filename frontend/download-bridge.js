(function installDSHShellDownloadBridge(allowedOrigin) {
  'use strict';

  if (location.origin !== allowedOrigin) return;
  if (window.__DSH_SHELL_DOWNLOAD_BRIDGE__?.origin === allowedOrigin) return;

  const MESSAGE_TYPE = 'dsh-shell:download-request';

  function downloadURL(value) {
    try {
      const url = new URL(String(value || ''), location.href);
      if (url.origin !== allowedOrigin || url.pathname !== '/api/session.export') return null;
      const keys = [...url.searchParams.keys()];
      if (keys.length !== 2 ||
          url.searchParams.getAll('sessionId').length !== 1 ||
          !url.searchParams.get('sessionId') ||
          url.searchParams.getAll('includeDescendants').length !== 1 ||
          url.searchParams.get('includeDescendants') !== 'true') return null;
      return url;
    } catch {
      return null;
    }
  }

  function send(urlValue, filename) {
    const url = downloadURL(urlValue);
    const invoke = window._wails?.invoke;
    if (url === null || typeof invoke !== 'function') return false;

    invoke(JSON.stringify({
      type: MESSAGE_TYPE,
      version: 1,
      url: url.toString(),
      filename: typeof filename === 'string' ? filename : '',
    }));
    return true;
  }

  const originalAnchorClick = HTMLAnchorElement.prototype.click;
  HTMLAnchorElement.prototype.click = function shellDownloadClick() {
    if (send(this.href, this.download)) return;
    return Reflect.apply(originalAnchorClick, this, arguments);
  };

  document.addEventListener('click', (event) => {
    const anchor = event.composedPath().find((node) => node instanceof HTMLAnchorElement);
    if (!(anchor instanceof HTMLAnchorElement) || !send(anchor.href, anchor.download)) return;
    event.preventDefault();
    event.stopImmediatePropagation();
  }, true);

  const originalOpen = window.open;
  window.open = function shellDownloadOpen(url, target, features) {
    if (send(url, '')) return null;
    return Reflect.apply(originalOpen, this, arguments);
  };

  Object.defineProperty(window, '__DSH_SHELL_DOWNLOAD_BRIDGE__', {
    value: Object.freeze({ origin: allowedOrigin, version: 1 }),
    configurable: false,
    enumerable: false,
    writable: false,
  });
})
