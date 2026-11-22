import { decodeFilterOptions, computeFilterOptionsClientSide } from '../kgFilterCascade';

// Identity nodes key on the login (#41505), so the Node dropdown's label — the key's
// name segment — needs the sparse display-name column to show a human name at all.
// Two GitLab accounts of one person: different logins, display names differing only
// in case. A third node has no display name, to pin the column staying sparse.
const ACC_GL = '33333333-3333-3333-3333-333333333333';
const KEY_WORK = 'gitlab:GitLab-test::UserAccount::mangglesh.dagar';
const KEY_PERSONAL = 'gitlab:GitLab-test::UserAccount::mangglesh1653';
const KEY_REPO = 'gitlab:GitLab-test::Repository::widgets';

const payload = {
  account_ids: [ACC_GL],
  node_types: { Repository: ['GitLabProject'], UserAccount: ['GitLabUser'] },
  label_keys: [],
  attribute_keys: [],
  specific_type_dict: ['GitLabUser', 'GitLabProject'],
  cluster_dict: [],
  node_keys: [KEY_WORK, KEY_PERSONAL, KEY_REPO],
  node_ids: ['n-work', 'n-personal', 'n-repo'],
  node_account_idx: [0, 0, 0],
  node_specific_type_idx: [0, 0, 1],
  node_cluster_idx: [-1, -1, -1],
  node_bucket_idx: [0, 0, 1],
  // Sparse: indices 0 and 1 only — the repository node carries no display name.
  display_name_node_idx: [0, 1],
  display_names: ['mangglesh dagar', 'Mangglesh Dagar'],
  filter_buckets: [
    [0, 1, 0],
    [0, 0, 1],
  ],
  label_key_buckets: [],
  attribute_key_buckets: [],
  node_count: 3,
};

describe('decodeFilterOptions — display names', () => {
  it('maps each sparse display name onto its own node key', () => {
    const decoded = decodeFilterOptions(payload);
    expect(decoded.nodeDisplayNameMap).toEqual({
      [KEY_WORK]: 'mangglesh dagar',
      [KEY_PERSONAL]: 'Mangglesh Dagar',
    });
  });

  it('leaves non-identity nodes out of the map', () => {
    const decoded = decodeFilterOptions(payload);
    expect(decoded.nodeDisplayNameMap[KEY_REPO]).toBeUndefined();
  });

  it('decodes a payload with no display-name column (a pre-#41505 cache row)', () => {
    const withoutColumn = { ...payload };
    delete withoutColumn.display_name_node_idx;
    delete withoutColumn.display_names;
    expect(decodeFilterOptions(withoutColumn).nodeDisplayNameMap).toEqual({});
  });

  it('ignores an index that points past the node columns', () => {
    const decoded = decodeFilterOptions({ ...payload, display_name_node_idx: [0, 99], display_names: ['a', 'b'] });
    expect(decoded.nodeDisplayNameMap).toEqual({ [KEY_WORK]: 'a' });
  });
});

describe('computeFilterOptionsClientSide — display names', () => {
  it('keeps the display name of every node that survives scoping', () => {
    const decoded = decodeFilterOptions(payload);
    const scoped = computeFilterOptionsClientSide(decoded, { broadNodeTypes: ['UserAccount'] });
    expect(Object.keys(scoped.nodeIdMap).sort()).toEqual([KEY_WORK, KEY_PERSONAL]);
    expect(scoped.nodeDisplayNameMap).toEqual({
      [KEY_WORK]: 'mangglesh dagar',
      [KEY_PERSONAL]: 'Mangglesh Dagar',
    });
  });

  it('drops the display names of nodes scoping filtered out', () => {
    const decoded = decodeFilterOptions(payload);
    const scoped = computeFilterOptionsClientSide(decoded, { broadNodeTypes: ['Repository'] });
    expect(scoped.nodeDisplayNameMap).toEqual({});
  });
});
