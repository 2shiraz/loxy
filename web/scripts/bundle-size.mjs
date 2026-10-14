// Initial-load bundle budget (§1.4): gzip size of JS + CSS needed for first paint
// must stay <= 150 KB. Approximation: everything index.html references, the
// entry/* files, the root layout node (nodes/0*), and the static import graph
// reachable from them (via `import ... from "./x.js"` in each chunk). uPlot is
// only ever loaded dynamically, so it must never be reachable here.
import { readFile, readdir } from 'node:fs/promises';
import { existsSync } from 'node:fs';
import { join, dirname, resolve, basename } from 'node:path';
import { gzipSync } from 'node:zlib';

const BUILD = new URL('../build/', import.meta.url).pathname;
const IMM = join(BUILD, '_app/immutable');
const BUDGET = 150 * 1024;

if (!existsSync(join(BUILD, 'index.html'))) {
  console.error('bundle-size: build/index.html missing — run `npm run build` first');
  process.exit(2);
}

const html = await readFile(join(BUILD, 'index.html'), 'utf8');
const seeds = new Set();
for (const m of html.matchAll(/(?:href|src|import\()\s*=?\s*["']([^"']+\.(?:js|css))["']/g)) {
  const p = m[1].startsWith('/') ? join(BUILD, m[1]) : resolve(BUILD, m[1]);
  if (existsSync(p)) seeds.add(p);
}
for (const m of html.matchAll(/import\(["']([^"']+\.js)["']\)/g)) {
  const p = m[1].startsWith('/') ? join(BUILD, m[1]) : resolve(BUILD, m[1]);
  if (existsSync(p)) seeds.add(p);
}
for (const d of ['entry', 'nodes']) {
  const dir = join(IMM, d);
  if (!existsSync(dir)) continue;
  for (const f of await readdir(dir)) {
    if (d === 'entry' || /^0\./.test(f)) seeds.add(join(dir, f));
  }
}

const seen = new Set();
const queue = [...seeds];
while (queue.length) {
  const f = queue.shift();
  if (seen.has(f) || !existsSync(f)) continue;
  seen.add(f);
  if (!f.endsWith('.js')) continue;
  const src = await readFile(f, 'utf8');
  // static imports only: `import"./a.js"`, `from"./a.js"`, `import {x} from "../b.js"`
  for (const m of src.matchAll(/(?:\bfrom\s*|\bimport\s*)["'](\.{1,2}\/[^"']+\.(?:js|css))["']/g)) {
    queue.push(resolve(dirname(f), m[1]));
  }
}

let total = 0;
const rows = [];
for (const f of seen) {
  if (!/\.(js|css)$/.test(f)) continue;
  const buf = await readFile(f);
  if (/uplot/i.test(basename(f)) || (f.endsWith('.js') && buf.includes('u-over'))) {
    console.error(`bundle-size: uPlot leaked into the initial bundle via ${f}`);
    process.exit(1);
  }
  const gz = gzipSync(buf, { level: 9 }).length;
  total += gz;
  rows.push([f.replace(BUILD, ''), gz]);
}
// CSS assets referenced by the root layout are emitted under assets/
const assets = join(IMM, 'assets');
if (existsSync(assets)) {
  for (const f of await readdir(assets)) {
    if (/^0\..*\.css$/.test(f) || /^_layout\..*\.css$/.test(f)) {
      const p = join(assets, f);
      if (seen.has(p)) continue;
      const gz = gzipSync(await readFile(p), { level: 9 }).length;
      total += gz;
      rows.push([p.replace(BUILD, ''), gz]);
    }
  }
}

rows.sort((a, b) => b[1] - a[1]);
for (const [f, s] of rows) console.log(`${(s / 1024).toFixed(1).padStart(7)} KB  ${f}`);
console.log(`${'-'.repeat(40)}\n${(total / 1024).toFixed(1).padStart(7)} KB  initial JS+CSS (gzip), budget ${BUDGET / 1024} KB`);
if (total > BUDGET) {
  console.error('bundle-size: over budget');
  process.exit(1);
}
