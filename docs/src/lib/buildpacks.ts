// Reads the buildpack.toml files at build time, so the configuration
// reference is generated from the same source the buildpacks ship with.
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { parse } from 'smol-toml';

export interface Setting {
  name: string;
  description: string;
  default?: string;
  build: boolean;
  launch: boolean;
}

export interface Buildpack {
  dir: string;
  id: string;
  name: string;
  description: string;
  settings: Setting[];
}

// Astro builds from docs/; the repository root is one level up. (import.meta.url
// would point into the bundled output once pages are prerendered.)
const repo = resolve(process.cwd(), '..');

function load(dir: string): Buildpack {
  const raw = readFileSync(resolve(repo, 'buildpacks', dir, 'buildpack.toml'), 'utf8');
  const toml = parse(raw) as any;
  const settings = (toml.metadata?.configurations ?? []).map((c: any) => ({
    name: c.name,
    description: c.description,
    default: c.default,
    build: Boolean(c.build),
    launch: Boolean(c.launch),
  }));
  return {
    dir,
    id: toml.buildpack.id,
    name: toml.buildpack.name,
    description: toml.buildpack.description ?? '',
    settings,
  };
}

export const buildpacks: Buildpack[] = ['node-engine', 'openssl-fips', 'pnpm', 'pnpm-install'].map(load);

// The settings of every buildpack, each listed once with the buildpacks that
// read it (BP_NODE_PROJECT_PATH, for example, is read by several).
export function allSettings(): (Setting & { buildpacks: string[] })[] {
  const byName = new Map<string, Setting & { buildpacks: string[] }>();
  for (const bp of buildpacks) {
    for (const s of bp.settings) {
      const seen = byName.get(s.name);
      if (seen) seen.buildpacks.push(bp.id);
      else byName.set(s.name, { ...s, buildpacks: [bp.id] });
    }
  }
  return [...byName.values()];
}

export const version = readFileSync(resolve(repo, 'VERSION'), 'utf8').trim();
