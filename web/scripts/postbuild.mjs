// Post-build for the static SPA in build/:
// 1. Moves inline <script> blocks from HTML into hashed external files so the
//    panel works under a strict CSP (`script-src 'self'`, §14.3). SvelteKit's
//    boot script uses document.currentScript, which works for classic external scripts.
// 2. Writes precompressed .gz and .br siblings for text assets (node zlib only).
import { readdir, readFile, writeFile, stat } from 'node:fs/promises';
import { join, extname } from 'node:path';
import { createHash } from 'node:crypto';
import { gzipSync, brotliCompressSync, constants } from 'node:zlib';

const ROOT = new URL('../build/', import.meta.url).pathname;
const EXT = new Set(['.js', '.css', '.html', '.svg', '.json', '.txt']);

async function* walk(dir) {
  for (const e of await readdir(dir, { withFileTypes: true })) {
    const p = join(dir, e.name);
    if (e.isDirectory()) yield* walk(p);
    else yield p;
  }
}

let moved = 0;
for await (const f of walk(ROOT)) {
  if (extname(f) !== '.html') continue;
  let html = await readFile(f, 'utf8');
  const out = [];
  for (const m of html.matchAll(/<script>([\s\S]*?)<\/script>/g)) {
    const code = m[1].trim();
    const hash = createHash('sha256').update(code).digest('hex').slice(0, 10);
    const name = `_app/immutable/inline.${hash}.js`;
    await writeFile(join(ROOT, name), code + '\n');
    out.push([m[0], `<script src="/${name}"></script>`]);
  }
  for (const [a, b] of out) html = html.replace(a, b);
  if (out.length) { await writeFile(f, html); moved += out.length; }
}
console.log(`postbuild: externalised ${moved} inline script(s)`);

let n = 0;
for await (const f of walk(ROOT)) {
  if (!EXT.has(extname(f))) continue;
  const buf = await readFile(f);
  if ((await stat(f)).size < 256) continue;
  await writeFile(f + '.gz', gzipSync(buf, { level: 9 }));
  await writeFile(f + '.br', brotliCompressSync(buf, { params: { [constants.BROTLI_PARAM_QUALITY]: 11, [constants.BROTLI_PARAM_SIZE_HINT]: buf.length } }));
  n++;
}
console.log(`postbuild: wrote .gz and .br for ${n} files`);
