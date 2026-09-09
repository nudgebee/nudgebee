import fs from 'fs';
import path from 'path';

/**
 * The Optimise tab strip renders a tab's sub-tabs by indexing
 * `filterOptions[activeDropdownTab]`, so any tab carrying `tabOptions` must have
 * `value === its array index`. Inserting a tab renumbers everything after it,
 * and getting that wrong renders the wrong tab body with no error — which is why
 * this is asserted rather than left to review.
 *
 * Read as source rather than imported: the tab list is built inside a component,
 * behind feature flags and session access checks.
 */
const source = fs.readFileSync(path.join(process.cwd(), 'src/pages/optimise/index.jsx'), 'utf8');

const filterOptionsBlock = () => {
  const start = source.indexOf('const filterOptions = useMemo(');
  const end = source.indexOf('].filter(Boolean)', start);
  expect(start).toBeGreaterThan(-1);
  expect(end).toBeGreaterThan(start);
  return source.slice(start, end);
};

// Each entry, in declaration order, with whether it declares sub-tabs.
const parseTabs = () => {
  const block = filterOptionsBlock();
  const tabs: { name: string; value: number; hasSubTabs: boolean }[] = [];
  const nameRe = /name: '([^']+)'/g;
  let match: RegExpExecArray | null;
  while ((match = nameRe.exec(block)) !== null) {
    const rest = block.slice(match.index, nameRe.lastIndex + 900);
    const value = /value: (\d+)/.exec(rest);
    if (!value) continue;
    // A nested `tabOptions` array before the next top-level `name:` entry.
    const nextName = rest.indexOf("name: '", 10);
    const scope = nextName === -1 ? rest : rest.slice(0, nextName);
    tabs.push({ name: match[1], value: Number(value[1]), hasSubTabs: scope.includes('tabOptions:') });
  }
  return tabs;
};

describe('Optimise tab registration', () => {
  // Everything below reads the tab list out of source, so a formatting change
  // that defeats the parse would leave the other assertions passing over an
  // empty list — silently testing nothing. This is the guard: it fails loudly
  // instead, and is the signal to fix the parser (or lift the tab list into its
  // own module and import it).
  it('parses the tab list it is asserting on', () => {
    const tabs = parseTabs();
    expect(tabs.length).toBeGreaterThanOrEqual(6);
    expect(tabs.map((t) => t.name)).toEqual(expect.arrayContaining(['Summary', 'Cost', 'Resolutions', 'Configuration', 'Security', 'Auto Optimize']));
    expect(tabs.filter((t) => t.hasSubTabs).map((t) => t.name)).toEqual(['Security', 'Auto Optimize']);
  });

  it('gives every tab with sub-tabs a value equal to its position', () => {
    parseTabs().forEach((tab, index) => {
      if (!tab.hasSubTabs) return;
      expect({ tab: tab.name, value: tab.value }).toEqual({ tab: tab.name, value: index });
    });
  });

  it('numbers tabs uniquely and consecutively from zero', () => {
    const values = parseTabs().map((t) => t.value);
    expect(values).toEqual([...new Set(values)]);
    expect(values).toEqual([...values].sort((a, b) => a - b));
    expect(values[0]).toBe(0);
  });

  it('renders a body for every registered tab value', () => {
    const rendered = new Set([...source.matchAll(/activeTab === (\d+) &&/g)].map((m) => Number(m[1])));
    parseTabs().forEach((tab) => {
      expect({ tab: tab.name, rendered: rendered.has(tab.value) }).toEqual({ tab: tab.name, rendered: true });
    });
  });

  it('groups the finding tabs together and puts Resolutions after them', () => {
    // Cost, Configuration and Security are lists to triage; Resolutions is the
    // record of what was already actioned, so it follows rather than splits them.
    const names = parseTabs().map((t) => t.name);
    expect(names.slice(0, names.indexOf('Resolutions'))).toEqual(['Summary', 'Cost', 'Configuration', 'Security']);
  });
});
