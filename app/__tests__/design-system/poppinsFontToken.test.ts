import fs from 'fs';
import path from 'path';

/**
 * `app/CLAUDE.md` → Design System: "never hardcode a color/spacing/radius/font
 * that has a `--ds-*` token (use the `ds` object from `@utils/colors`)", and
 * `design-system.md` §1.5 assigns labels/headings/section titles to
 * `var(--ds-font-display)` — which resolves to Poppins.
 *
 * 71 hardcoded Poppins literals were migrated to `ds.font.display` /
 * `var(--ds-font-display)`. Asserted rather than left to review because a
 * hardcoded `fontFamily: 'Poppins'` renders identically to the token today, so
 * nothing at runtime, in review, or in any other test would catch the drift
 * coming back — it only shows up later as a font that no longer follows the
 * token when the token changes.
 *
 * `src/components/common/ds/**` is exempt: the primitives are the layer that
 * defines the token's meaning.
 */
const SRC = path.join(process.cwd(), 'src');
const EXEMPT = path.join('src', 'components', 'common', 'ds');

const walk = (dir: string, out: string[] = []): string[] => {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) walk(full, out);
    else if (/\.(tsx|jsx)$/.test(entry.name)) out.push(full);
  }
  return out;
};

// A quoted literal naming Poppins as the family, with or without a fallback:
// 'Poppins' · 'poppins' · 'Poppins, sans-serif' · '"Poppins", sans-serif' · `"Poppins", sans-serif`
const QUOTED = /(['"`])((?:\\.|(?!\1)[^\\])*)\1/g;
const isPoppinsLiteral = (body: string) => /^\s*['"]?\s*poppins\s*['"]?\s*(,\s*sans-serif\s*)?$/i.test(body);

const findViolations = () => {
  const hits: string[] = [];
  for (const file of walk(SRC)) {
    if (file.includes(EXEMPT)) continue;
    const lines = fs.readFileSync(file, 'utf8').split('\n');
    lines.forEach((line, i) => {
      if (!/fontFamily/i.test(line) || !/poppins/i.test(line)) return;
      for (const match of line.matchAll(QUOTED)) {
        if (isPoppinsLiteral(match[2])) {
          hits.push(`${path.relative(process.cwd(), file)}:${i + 1}`);
          return;
        }
      }
    });
  }
  return hits;
};

describe('design system — display font token', () => {
  it('has no hardcoded Poppins fontFamily literals outside ds/', () => {
    expect(findViolations()).toEqual([]);
  });

  it('detects a hardcoded literal when one is reintroduced', () => {
    // Guards the guard: if the matcher silently stopped matching, the assertion
    // above would pass on a tree full of violations.
    expect(isPoppinsLiteral('Poppins')).toBe(true);
    expect(isPoppinsLiteral('poppins')).toBe(true);
    expect(isPoppinsLiteral('Poppins, sans-serif')).toBe(true);
    expect(isPoppinsLiteral('"Poppins", sans-serif')).toBe(true);
    expect(isPoppinsLiteral('var(--ds-font-display)')).toBe(false);
    expect(isPoppinsLiteral('Roboto')).toBe(false);
  });
});
