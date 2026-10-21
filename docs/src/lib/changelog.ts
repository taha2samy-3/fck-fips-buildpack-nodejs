// Reads CHANGELOG.md at build time, so the versions page lists every release
// without being edited: a release cannot be cut without a changelog section
// (the release workflow refuses), and the docs rebuild when the file changes.
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

export interface Section {
  heading: string; // Added, Changed, Fixed, ...
  items: string[]; // rendered HTML
}

export interface Release {
  version: string; // "1.0.0" or "Unreleased"
  date?: string;
  summary: string[]; // rendered HTML paragraphs before the first ###
  sections: Section[];
}

const escape = (s: string) =>
  s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');

// The inline Markdown CHANGELOG.md uses: `code`, **bold** and [links](url).
export function inline(md: string): string {
  const codes: string[] = [];
  let s = md.replace(/`([^`]+)`/g, (_, c) => {
    codes.push(c);
    return `\u0000${codes.length - 1}\u0000`;
  });
  s = escape(s)
    .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
    .replace(/\[([^\]]+)\]\((https?:\/\/[^)\s]+)\)/g, '<a href="$2">$1</a>');
  return s.replace(/\u0000(\d+)\u0000/g, (_, i) => `<code>${escape(codes[Number(i)])}</code>`);
}

export function parse(md: string): Release[] {
  const releases: Release[] = [];
  let release: Release | undefined;
  let section: Section | undefined;
  let item: string[] | undefined;
  let paragraph: string[] = [];

  const flushItem = () => {
    if (item && section) section.items.push(inline(item.join(' ')));
    item = undefined;
  };
  const flushParagraph = () => {
    if (paragraph.length && release) release.summary.push(inline(paragraph.join(' ')));
    paragraph = [];
  };

  for (const raw of md.split('\n')) {
    const line = raw.trimEnd();
    const h2 = line.match(/^## \[([^\]]+)\](?: - (\d{4}-\d{2}-\d{2}))?/);
    if (h2) {
      flushItem();
      flushParagraph();
      release = { version: h2[1], date: h2[2], summary: [], sections: [] };
      section = undefined;
      releases.push(release);
      continue;
    }
    if (!release || /^\[[^\]]+\]: /.test(line)) continue; // preamble, link definitions
    const h3 = line.match(/^### (.+)/);
    if (h3) {
      flushItem();
      flushParagraph();
      section = { heading: h3[1], items: [] };
      release.sections.push(section);
      continue;
    }
    const bullet = line.match(/^- (.*)/);
    if (bullet && section) {
      flushItem();
      item = [bullet[1]];
    } else if (item && /^\s+\S/.test(line)) {
      item.push(line.trim());
    } else if (line.trim() === '') {
      flushItem();
      flushParagraph();
    } else if (!section) {
      paragraph.push(line.trim());
    }
  }
  flushItem();
  flushParagraph();
  return releases;
}

const all = parse(readFileSync(resolve(process.cwd(), '..', 'CHANGELOG.md'), 'utf8'));

// Released versions, newest first, and whatever is waiting for the next one.
export const releases = all.filter((r) => r.version !== 'Unreleased');
export const unreleased = all.find((r) => r.version === 'Unreleased' && r.sections.some((s) => s.items.length));
export const latest = releases[0];
