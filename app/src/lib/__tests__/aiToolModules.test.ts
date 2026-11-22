/**
 * llm-server decides which custom-role grant each AI tool needs from
 * llm/llm-server/tools/core/tool_modules.json. Every module it names must be one
 * the role editor can grant, or a tool would require a grant no admin can give.
 */
import fs from 'fs';
import path from 'path';
import * as yaml from 'js-yaml';
import { classifyAction, RUN_TIME_GRANTS } from '@lib/permissionCatalog';

function readActionNames(rel: string): string[] {
  const p = path.join(process.cwd(), rel);
  if (!fs.existsSync(p)) return [];
  const doc = yaml.load(fs.readFileSync(p, 'utf8')) as { actions?: Array<{ name?: string }> };
  return (doc?.actions || []).map((a) => a.name).filter((n): n is string => !!n);
}

describe('AI tool modules', () => {
  const toolModules = JSON.parse(fs.readFileSync(path.join(process.cwd(), '../llm/llm-server/tools/core/tool_modules.json'), 'utf8')) as {
    modules: Record<string, string[]>;
  };

  const grantable = new Set<string>(RUN_TIME_GRANTS.map((g) => g.module));
  for (const name of [...readActionNames('src/lib/actions.yaml'), ...readActionNames('src/ee/actions.yaml')]) {
    const c = classifyAction(name);
    if (c) grantable.add(c.module);
  }

  it('names only modules the role editor can grant', () => {
    const unknown: string[] = [];
    for (const [tool, modules] of Object.entries(toolModules.modules)) {
      for (const m of modules) {
        // integrations.<type> are per-type use grants, granted from the Integrations section.
        if (!m.startsWith('integrations.') && !grantable.has(m)) unknown.push(`${tool}: ${m}`);
      }
    }
    expect(unknown).toEqual([]);
  });
});
