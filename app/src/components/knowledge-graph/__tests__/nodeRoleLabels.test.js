import { NODE_ROLE_BOOKKEEPING_KEYS, canCarryRole, detectedRoleText, nodeRoleLabel, nodeRoleState } from '../nodeRoleLabels';

describe('nodeRoleLabel', () => {
  it('labels vocabulary roles and hides anything else', () => {
    expect(nodeRoleLabel('messagequeue')).toBe('Queue');
    expect(nodeRoleLabel('cache')).toBe('Cache');
    // A custom kind may describe itself with a free role; it is not a badge.
    expect(nodeRoleLabel('core-switch')).toBe('');
    expect(nodeRoleLabel(undefined)).toBe('');
  });
});

describe('nodeRoleState', () => {
  it('reads a user role and what was detected before it', () => {
    expect(nodeRoleState({ role: 'cache', engine: 'redis', role_source: 'user', detected_role: 'database' })).toEqual({
      role: 'cache',
      engine: 'redis',
      userSet: true,
      detectedRole: 'database',
    });
  });

  it('treats a role without a source as detected', () => {
    expect(nodeRoleState({ role: 'database', engine: 'postgres' })).toEqual({
      role: 'database',
      engine: 'postgres',
      userSet: false,
      detectedRole: 'database',
    });
  });

  it('ignores a non-vocabulary role and a non-text engine', () => {
    expect(nodeRoleState({ role: 'core-switch', engine: 3 })).toEqual({ role: '', engine: '', userSet: false, detectedRole: '' });
    expect(nodeRoleState(undefined)).toEqual({ role: '', engine: '', userSet: false, detectedRole: '' });
  });
});

it('lists the keys the details table hides', () => {
  expect(NODE_ROLE_BOOKKEEPING_KEYS).toEqual(['role_source', 'detected_role', 'detected_engine']);
});

it('lets the server decide while the target types are unknown', () => {
  expect(canCarryRole(null, 'Namespace')).toBe(true);
  expect(canCarryRole(['Workload'], 'Workload')).toBe(true);
  expect(canCarryRole(['Workload'], 'Namespace')).toBe(false);
});

it('shows a detected role outside the vocabulary as it is', () => {
  // A switch's free role a user overrode is still worth showing.
  expect(detectedRoleText('core-switch')).toBe('core-switch');
  expect(detectedRoleText('database')).toBe('Database');
  expect(detectedRoleText('')).toBe('');
});
