import assert from 'node:assert/strict';
import test from 'node:test';

import { renderNfpmConfig } from './package-linux.mjs';

test('renders every Linux architecture placeholder', () => {
  const rendered = renderNfpmConfig(
    'arch: "__GOARCH__"\nsrc: "./build/payload/linux-__GOARCH__/"\n',
    'amd64',
  );

  assert.equal(
    rendered,
    'arch: "amd64"\nsrc: "./build/payload/linux-amd64/"\n',
  );
});

test('rejects templates without an architecture placeholder', () => {
  assert.throws(
    () => renderNfpmConfig('arch: "amd64"\n', 'amd64'),
    /missing __GOARCH__/,
  );
});

test('rejects unsafe architecture values', () => {
  assert.throws(
    () => renderNfpmConfig('arch: "__GOARCH__"\n', '../amd64'),
    /invalid Linux architecture/,
  );
});
