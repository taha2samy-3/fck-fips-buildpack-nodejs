import { defineConfig } from 'astro/config';

// GitHub Pages serves a project site under /<repository>/. In CI the docs
// workflow passes the real values from actions/configure-pages, so renaming
// the repository cannot break asset paths; locally the defaults apply.
export default defineConfig({
  site: process.env.DOCS_SITE || 'https://taha2samy-3.github.io',
  base: process.env.DOCS_BASE || '/fck-fips-buildpack-nodejs',
  trailingSlash: 'always',
});
