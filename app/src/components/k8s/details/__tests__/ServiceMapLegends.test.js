import fs from 'fs';
import path from 'path';

import { relationshipTypeCategories, relationshipTypeLabel } from '../ServiceMapLegends';
import { snakeToTitleCase } from '@utils/common';

const GO_TYPES_FILE = path.resolve(__dirname, '../../../../../../api-server/services/knowledge_graph/core/types.go');

// Every RelationshipType the backend can send, read out of the Go source rather than copied,
// so adding a type there and forgetting this legend fails here instead of on a user's screen.
const backendRelationshipTypes = () => {
  let src;
  try {
    src = fs.readFileSync(GO_TYPES_FILE, 'utf8');
  } catch (err) {
    // Deliberately a failure and not a skip. This test exists because a hand-kept copy of
    // the backend's relationship types drifted; a version that quietly passes when it cannot
    // read them would restore exactly the blind spot it was written to close. Every workflow
    // in this monorepo checks the whole repo out, so an unreadable file is a real problem.
    throw new Error(
      `Cannot read the backend relationship types at ${GO_TYPES_FILE}: ${err.message}. ` +
        'This check must not be skipped — fix the path rather than ignoring it.'
    );
  }
  const block = src.slice(src.indexOf('type RelationshipType string'));
  const constBlock = block.slice(block.indexOf('const ('), block.indexOf('\n)'));
  const types = [];
  for (const line of constBlock.split('\n')) {
    // Skip commented-out (deprecated) entries so they are not required in the legend.
    if (/^\s*\/\//.test(line)) continue;
    const m = line.match(/RelationshipType\s*=\s*"([A-Z_]+)"/);
    if (m) types.push(m[1]);
  }
  return types;
};

const legendTypes = () => relationshipTypeCategories.flatMap((c) => c.items.map((i) => i.type));

describe('ServiceMapLegends relationship legend', () => {
  it('reads the relationship types out of the Go source', () => {
    const types = backendRelationshipTypes();
    // Guard the parser itself: if the Go file moves or its shape changes, fail loudly here
    // rather than silently asserting against an empty list.
    expect(types.length).toBeGreaterThan(30);
    expect(types).toContain('CALLS');
    expect(types).toContain('USES_IMAGE');
  });

  it('lists every relationship type the backend can send', () => {
    const missing = backendRelationshipTypes().filter((t) => !legendTypes().includes(t));
    expect(missing).toEqual([]);
  });

  it('lists no type the backend does not define', () => {
    const backend = backendRelationshipTypes();
    const unknown = legendTypes().filter((t) => !backend.includes(t));
    expect(unknown).toEqual([]);
  });

  it('covers the types the bug report saw on the graph', () => {
    // From the graphs of the K8s and AWS test accounts, including the four whose edges a user
    // could hover but not look up.
    const seenOnGraph = [
      'USES_IMAGE',
      'USES_SECRET',
      'USES_CONFIG',
      'USES_SERVICE_ACCOUNT',
      'ROUTES_TO',
      'ROUTES_TO_SERVICE',
      'RUNS_AS',
      'RUNS_IN',
      'MANAGES',
      'PUBLISHES_TO',
      'ASSOCIATED_WITH',
      'BELONGS_TO',
      'RUNS_ON',
      'CALLS',
      'MOUNTS',
      'IS_BOUND_TO',
      'EXPOSES',
      'IS_CONFIGURED_BY',
      'BUILT_FROM',
      'EMITS_LOGS_TO',
      'IS_ENCRYPTED_BY',
      'HOSTED_ON',
      'ROUTES_THROUGH',
    ];
    expect(legendTypes()).toEqual(expect.arrayContaining(seenOnGraph));
  });

  it('names each type the way the graph labels its edges', () => {
    // KnowledgeGraph.jsx builds an edge's label with snakeToTitleCase(type.toLowerCase()),
    // so the legend has to resolve to the same string for a user to match the two.
    for (const type of legendTypes()) {
      expect(relationshipTypeLabel(type)).toBe(snakeToTitleCase(type.toLowerCase()));
    }
    expect(relationshipTypeLabel('USES_IMAGE')).toBe('Uses Image');
    expect(relationshipTypeLabel('USES_SERVICE_ACCOUNT')).toBe('Uses Service Account');
  });

  it('gives every entry a description and lists each type once', () => {
    const types = legendTypes();
    expect(new Set(types).size).toBe(types.length);
    for (const cat of relationshipTypeCategories) {
      expect(cat.category).toBeTruthy();
      for (const item of cat.items) {
        expect(item.desc).toBeTruthy();
      }
    }
  });

  it('explains RUNS_ON in a way that covers the namespace edge it also draws', () => {
    const runsOn = legendTypes().includes('RUNS_ON') && relationshipTypeCategories.flatMap((c) => c.items).find((i) => i.type === 'RUNS_ON');
    expect(runsOn.desc).toMatch(/namespace/i);
  });
});
