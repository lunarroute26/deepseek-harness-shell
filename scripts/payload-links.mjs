import {
  chmodSync,
  copyFileSync,
  lstatSync,
  readdirSync,
  readlinkSync,
  rmSync,
  statSync,
} from 'node:fs';
import { dirname, join, resolve } from 'node:path';

export function walkLinks(root) {
  const links = [];
  const visit = directory => {
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      const path = join(directory, entry.name);
      const stat = lstatSync(path);
      if (stat.isSymbolicLink()) links.push(path);
      else if (stat.isDirectory()) visit(path);
    }
  };
  visit(root);
  return links;
}

export function materializeFileLinks(root) {
  let materialized = 0;
  for (const link of walkLinks(root)) {
    const target = resolve(dirname(link), readlinkSync(link));
    const targetStat = statSync(target);
    if (targetStat.isDirectory()) {
      throw new Error(`directory link remains in physical payload: ${link} -> ${target}`);
    }
    rmSync(link, { force: true });
    copyFileSync(target, link);
    chmodSync(link, targetStat.mode & 0o777);
    materialized += 1;
  }
  return materialized;
}
