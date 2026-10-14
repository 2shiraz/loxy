# ADR 0002 — CSP for the embedded SPA

Status: accepted · 2026-10-14

`default-src 'self'` (§14.3) blocks SvelteKit's inline boot script and inline `style` attributes. The build (`web/scripts/postbuild.mjs`) moves the inline script into a hashed file, and components use `style:` directives (CSSOM), so scripts need nothing extra. uPlot sets inline styles on its own elements, so the policy is:

`default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'`

`'unsafe-inline'` for styles only is a small risk (no script execution). Revisit if uPlot is replaced.
