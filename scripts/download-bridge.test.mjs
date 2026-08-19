import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import vm from 'node:vm';

const source = await readFile(new URL('../frontend/download-bridge.js', import.meta.url), 'utf8');

function bridgeHarness({ invoke = true } = {}) {
  let originalAnchorClicks = 0;
  let originalWindowOpens = 0;
  const messages = [];

  class FakeAnchor {
    constructor(href, download = '') {
      this.href = href;
      this.download = download;
    }
  }
  FakeAnchor.prototype.click = function click() {
    originalAnchorClicks += 1;
  };

  const window = {
    open() {
      originalWindowOpens += 1;
      return { opened: true };
    },
  };
  if (invoke) {
    window._wails = {
      invoke(message) {
        messages.push(JSON.parse(message));
      },
    };
  }

  const context = vm.createContext({
    document: { addEventListener() {} },
    HTMLAnchorElement: FakeAnchor,
    location: {
      href: 'http://127.0.0.1:62341/session/1',
      origin: 'http://127.0.0.1:62341',
    },
    URL,
    window,
  });
  vm.runInContext(`${source}("http://127.0.0.1:62341");`, context);
  return {
    context,
    messages,
    originalAnchorClicks: () => originalAnchorClicks,
    originalWindowOpens: () => originalWindowOpens,
  };
}

test('intercepts the detached anchor used by the upstream Session log controller', () => {
  const harness = bridgeHarness();
  const anchor = new harness.context.HTMLAnchorElement(
    'http://127.0.0.1:62341/api/session.export?sessionId=a&includeDescendants=true',
    'dsh-session-a.zip',
  );
  anchor.click();

  assert.equal(harness.originalAnchorClicks(), 0);
  assert.deepEqual(harness.messages, [{
    type: 'dsh-shell:download-request',
    version: 1,
    url: 'http://127.0.0.1:62341/api/session.export?sessionId=a&includeDescendants=true',
    filename: 'dsh-session-a.zip',
  }]);
});

test('leaves unrelated navigation and downloads under upstream control', () => {
  const harness = bridgeHarness();
  const anchor = new harness.context.HTMLAnchorElement('https://example.com/report.zip', 'report.zip');
  anchor.click();
  const opened = harness.context.window.open('https://example.com');

  assert.equal(harness.originalAnchorClicks(), 1);
  assert.equal(harness.originalWindowOpens(), 1);
  assert.deepEqual(opened, { opened: true });
  assert.deepEqual(harness.messages, []);
});

test('falls back to the WebView download when the native bridge is unavailable', () => {
  const harness = bridgeHarness({ invoke: false });
  const anchor = new harness.context.HTMLAnchorElement(
    'http://127.0.0.1:62341/api/session.export?sessionId=a&includeDescendants=true',
    'dsh-session-a.zip',
  );
  anchor.click();

  assert.equal(harness.originalAnchorClicks(), 1);
  assert.deepEqual(harness.messages, []);
});

test('leaves unknown session export contracts under upstream control', () => {
  const harness = bridgeHarness();
  const anchor = new harness.context.HTMLAnchorElement(
    'http://127.0.0.1:62341/api/session.export?sessionId=a&includeDescendants=true&format=zip',
    'dsh-session-a.zip',
  );
  anchor.click();

  assert.equal(harness.originalAnchorClicks(), 1);
  assert.deepEqual(harness.messages, []);
});
