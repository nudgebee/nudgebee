import { useEffect, useMemo, useState } from 'react';
import PropTypes from 'prop-types';
import { Box, Typography } from '@mui/material';
import RefreshIcon from '@mui/icons-material/Refresh';
import PeopleOutlineIcon from '@mui/icons-material/PeopleOutline';
import WarningAmberIcon from '@mui/icons-material/WarningAmber';
import { Banner } from '@ui/Banner';
import { Button } from '@ui/Button';
import { Input } from '@ui/Input';
import { Label } from '@ui/Label';
import { Link } from '@ui/Link';
import { Modal } from '@ui/Modal';
import { Select } from '@ui/Select';
import { ToggleGroup } from '@ui/ToggleGroup';
import Tooltip from '@ui/Tooltip';
import ThreeDotsMenu from '@ui/ThreeDotsMenu';
import { toast as snackbar } from '@ui/Toast';
import { DeleteIconRed as deleteIcon } from '@assets';
import { MemoryTable } from '@components/llm/MemoryTable';
import apiKnowledgeBase from '@api1/knowledge-base';
import apiUserManagement from '@api1/user';
import { ds } from '@utils/colors';

// Skill sources (#41513): a GitHub repository (optionally one folder / branch of
// it) whose Agent Skills folders — one SKILL.md each — become read-only
// organisation skills. The source decides who can use them, so its synced
// skills have no visibility of their own to change.

const SCOPE_ORGANISATION = 'organisation';
const SCOPE_SELECTED_ACCOUNTS = 'selected_accounts';
const SCOPE_OPTIONS = [
  { value: SCOPE_ORGANISATION, label: 'Organisation' },
  { value: SCOPE_SELECTED_ACCOUNTS, label: 'Selected accounts' },
];

export const shortCommit = (sha) => (sha ? String(sha).slice(0, 7) : '');

const formatSyncedAt = (dateString) =>
  dateString
    ? new Date(dateString).toLocaleDateString('en-US', { year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
    : 'Never';

const formatBytes = (size) => {
  if (size == null) return '—';
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
  return `${(size / (1024 * 1024)).toFixed(1)} MB`;
};

const pluralSkills = (n) => `${n} skill${n === 1 ? '' : 's'}`;

const REQUIREMENT_KIND_LABELS = { cli: 'CLI', mcp: 'MCP server', runtime: 'runtime', python: 'Python package' };

const SOURCE_STATUS = {
  pending: { text: 'Pending', tone: 'neutral' },
  syncing: { text: 'Syncing', tone: 'warning' },
  active: { text: 'Synced', tone: 'success' },
  error: { text: 'Sync failed', tone: 'critical' },
};

// The organisation's accounts, for the "Selected accounts" picker. Loaded only
// while a form that shows the picker is open.
const useTenantAccountOptions = (open) => {
  const [accounts, setAccounts] = useState([]);
  const [loading, setLoading] = useState(false);
  useEffect(() => {
    if (!open) return undefined;
    let active = true;
    setLoading(true);
    apiUserManagement
      .listAccounts()
      .then((rows) => active && setAccounts(Array.isArray(rows) ? rows : []))
      .catch(() => active && setAccounts([]))
      .finally(() => active && setLoading(false));
    return () => {
      active = false;
    };
  }, [open]);
  const options = useMemo(
    () => accounts.map((a) => ({ value: a.id, label: a.account_name || a.id, group: a.cloud_provider || 'Other' })),
    [accounts]
  );
  return { options, loading };
};

// Who can use a source's skills — the same choice and copy as an integration's
// knowledge base (KnowledgeBaseSharingModal).
const SkillSourceScopeFields = ({ visibility, onVisibilityChange, accountIds, onAccountIdsChange, accountOptions, accountsLoading }) => (
  <Box>
    <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)', mb: ds.space[1] }}>
      Who can use its skills
    </Typography>
    <ToggleGroup
      selection='single'
      size='sm'
      ariaLabel='Who can use the skills of this source'
      value={visibility}
      onChange={(next) => next && onVisibilityChange(next)}
      options={SCOPE_OPTIONS}
    />
    <Typography sx={{ marginTop: ds.space[2], fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)' }}>
      {visibility === SCOPE_SELECTED_ACCOUNTS
        ? 'Only the accounts you pick use its skills.'
        : 'Every account in the organisation uses its skills. An account admin can switch a skill off for their account.'}
    </Typography>
    {visibility === SCOPE_SELECTED_ACCOUNTS && (
      <Box sx={{ marginTop: ds.space[3] }}>
        <Select
          id='skill-source-accounts'
          label='Accounts'
          placeholder='Select accounts'
          multiple
          grouped
          searchable
          options={accountOptions}
          value={accountIds}
          onChange={(next) => onAccountIdsChange(Array.isArray(next) ? next : [])}
          loading={accountsLoading}
        />
      </Box>
    )}
  </Box>
);
SkillSourceScopeFields.propTypes = {
  visibility: PropTypes.string.isRequired,
  onVisibilityChange: PropTypes.func.isRequired,
  accountIds: PropTypes.arrayOf(PropTypes.string).isRequired,
  onAccountIdsChange: PropTypes.func.isRequired,
  accountOptions: PropTypes.array,
  accountsLoading: PropTypes.bool,
};

export const AddSkillSourceModal = ({ open, onClose, onCreated }) => {
  const [repo, setRepo] = useState('');
  const [folder, setFolder] = useState('');
  const [branch, setBranch] = useState('');
  const [visibility, setVisibility] = useState(SCOPE_ORGANISATION);
  const [accountIds, setAccountIds] = useState([]);
  const [saving, setSaving] = useState(false);
  const { options: accountOptions, loading: accountsLoading } = useTenantAccountOptions(open);

  useEffect(() => {
    if (!open) return;
    setRepo('');
    setFolder('');
    setBranch('');
    setVisibility(SCOPE_ORGANISATION);
    setAccountIds([]);
  }, [open]);

  const handleSubmit = async () => {
    if (!repo.trim()) {
      snackbar.error('Enter the GitHub repository to sync skills from');
      return;
    }
    if (visibility === SCOPE_SELECTED_ACCOUNTS && accountIds.length === 0) {
      snackbar.error('Pick at least one account to use its skills');
      return;
    }
    setSaving(true);
    try {
      const response = await apiKnowledgeBase.createSkillSource({
        repo: repo.trim(),
        folder: folder.trim(),
        branch: branch.trim(),
        visibility,
        accountIds: visibility === SCOPE_SELECTED_ACCOUNTS ? accountIds : [],
      });
      if (response.errors && response.errors.length > 0) {
        snackbar.error(response.errors[0]?.message || 'Failed to add the repository');
        return;
      }
      snackbar.success(`Added ${response.data?.repo || repo.trim()}. Its first sync has started.`);
      onCreated?.(response.data);
      onClose();
    } catch (err) {
      console.error('Error adding skill source:', err);
      snackbar.error('An error occurred while adding the repository');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      open={open}
      handleClose={onClose}
      title='Add repository'
      subtitle='Every folder with a SKILL.md becomes a skill. Private repositories use your GitHub integration.'
      width='sm'
      confirmText='Add repository'
      onConfirm={handleSubmit}
      confirmDisabled={saving || !repo.trim()}
      loader={saving}
      backdropClickClose={!saving}
    >
      <Box sx={{ padding: ds.space[5], display: 'flex', flexDirection: 'column', gap: ds.space[4] }} data-testid='skill-source-add-modal'>
        <Input
          size='sm'
          label='Repository'
          required
          placeholder='owner/name or https://github.com/owner/name'
          value={repo}
          onChange={setRepo}
          id='skill-source-repo'
        />
        <Input size='sm' label='Folder' placeholder='skills' value={folder} onChange={setFolder} id='skill-source-folder' />
        <Input size='sm' label='Branch' placeholder='default branch' value={branch} onChange={setBranch} id='skill-source-branch' />
        <SkillSourceScopeFields
          visibility={visibility}
          onVisibilityChange={setVisibility}
          accountIds={accountIds}
          onAccountIdsChange={setAccountIds}
          accountOptions={accountOptions}
          accountsLoading={accountsLoading}
        />
      </Box>
    </Modal>
  );
};
AddSkillSourceModal.propTypes = {
  open: PropTypes.bool.isRequired,
  onClose: PropTypes.func.isRequired,
  onCreated: PropTypes.func,
};

const SkillSourceVisibilityModal = ({ source, onClose, onSaved }) => {
  const open = Boolean(source);
  const [visibility, setVisibility] = useState(SCOPE_ORGANISATION);
  const [accountIds, setAccountIds] = useState([]);
  const [saving, setSaving] = useState(false);
  const { options: accountOptions, loading: accountsLoading } = useTenantAccountOptions(open);

  useEffect(() => {
    if (!source) return;
    setVisibility(source.visibility === SCOPE_SELECTED_ACCOUNTS ? SCOPE_SELECTED_ACCOUNTS : SCOPE_ORGANISATION);
    setAccountIds(source.account_ids || []);
  }, [source]);

  const handleSave = async () => {
    if (visibility === SCOPE_SELECTED_ACCOUNTS && accountIds.length === 0) {
      snackbar.error('Pick at least one account to use its skills');
      return;
    }
    setSaving(true);
    try {
      const response = await apiKnowledgeBase.updateSkillSource(source.id, visibility, accountIds);
      if (response.errors && response.errors.length > 0) {
        snackbar.error(response.errors[0]?.message || 'Failed to change who can use the repository');
        return;
      }
      snackbar.success(
        visibility === SCOPE_SELECTED_ACCOUNTS
          ? `Skills from ${source.repo} are now used by the selected accounts only`
          : `Skills from ${source.repo} are now used by every account`
      );
      onSaved?.();
      onClose();
    } catch (err) {
      console.error('Error changing skill source visibility:', err);
      snackbar.error('An error occurred while changing who can use the repository');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      open={open}
      handleClose={onClose}
      title='Who can use it'
      subtitle={source ? `Accounts that use the skills synced from ${source.repo}.` : ''}
      width='sm'
      confirmText='Save'
      onConfirm={handleSave}
      confirmDisabled={saving || accountsLoading}
      loader={saving}
    >
      <Box sx={{ padding: ds.space[5] }} data-testid='skill-source-visibility-modal'>
        <SkillSourceScopeFields
          visibility={visibility}
          onVisibilityChange={setVisibility}
          accountIds={accountIds}
          onAccountIdsChange={setAccountIds}
          accountOptions={accountOptions}
          accountsLoading={accountsLoading}
        />
      </Box>
    </Modal>
  );
};
SkillSourceVisibilityModal.propTypes = {
  source: PropTypes.object,
  onClose: PropTypes.func.isRequired,
  onSaved: PropTypes.func,
};

// Skills a sync left out (no SKILL.md frontmatter, a name clash, too large…),
// collapsed behind a count.
const SkippedSkills = ({ skipped }) => {
  const [open, setOpen] = useState(false);
  if (!skipped || skipped.length === 0) return null;
  return (
    <Box sx={{ mt: ds.space[1] }}>
      <Button tone='link' size='sm' onClick={() => setOpen((v) => !v)} aria-expanded={open} data-testid='skill-source-skipped-toggle'>
        {`${skipped.length} skipped`}
      </Button>
      {open && (
        <Box component='ul' sx={{ m: 0, mt: ds.space[1], pl: ds.space[4] }}>
          {skipped.map((s) => (
            <Box component='li' key={`${s.source_path}-${s.reason}`} sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)' }}>
              <Box component='span' sx={{ fontFamily: 'var(--ds-font-mono)' }}>
                {s.source_path || '(repository root)'}
              </Box>
              {` — ${s.reason}`}
            </Box>
          ))}
        </Box>
      )}
    </Box>
  );
};
SkippedSkills.propTypes = { skipped: PropTypes.array };

// Something the skill needs that agents here don't have (a CLI, MCP server, runtime or Python package).
const hasMissingRequirement = (kb) => (kb.skill_meta?.requirements || []).some((r) => r.kind !== 'note' && r.provided === false);

// The skills one repository brought in, like an integration's documents list.
// Each opens its details; skills the last sync left out are listed after them.
const SourceSkillsModal = ({ source, skills, onClose, onOpenSkill, renderSkillSwitch }) => {
  const rows = useMemo(() => [...skills].sort((a, b) => (a.name || '').localeCompare(b.name || '')), [skills]);
  const skipped = source?.sync_report?.skipped || [];
  return (
    <Modal open={Boolean(source)} handleClose={onClose} title={`Skills from ${source?.repo || ''}`} width='md'>
      <Box sx={{ padding: ds.space[5], display: 'flex', flexDirection: 'column', gap: ds.space[3] }} data-testid='source-skills-modal'>
        {rows.length === 0 ? (
          <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)' }}>
            {source?.skill_count > 0 ? 'None of these skills are used by this account.' : 'No skills from this repository yet.'}
          </Typography>
        ) : (
          <MemoryTable
            columns={[
              {
                key: 'name',
                label: 'Skill',
                type: 'text-long',
                // The clamped description gives the column no natural width, so
                // without one it shrinks to a sliver next to the switch.
                width: renderSkillSwitch ? '78%' : '100%',
                render: (kb) => (
                  <Box sx={{ minWidth: 0 }}>
                    <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1] }}>
                      <Button tone='link' size='sm' onClick={() => onOpenSkill(kb)} data-testid='source-skill-open'>
                        {kb.name}
                      </Button>
                      {hasMissingRequirement(kb) && (
                        <Tooltip title='Needs something agents here do not have. Open the skill to see what.' placement='top'>
                          <WarningAmberIcon
                            sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-amber-600)' }}
                            data-testid='source-skill-warning'
                          />
                        </Tooltip>
                      )}
                    </Box>
                    {kb.description && (
                      <Typography
                        title={kb.description}
                        sx={{
                          fontSize: 'var(--ds-text-caption)',
                          color: 'var(--ds-gray-500)',
                          display: '-webkit-box',
                          WebkitLineClamp: 2,
                          WebkitBoxOrient: 'vertical',
                          overflow: 'hidden',
                        }}
                      >
                        {kb.description}
                      </Typography>
                    )}
                  </Box>
                ),
              },
              ...(renderSkillSwitch ? [{ key: 'enabled', label: 'Use in answers', type: 'status', width: '130px', render: renderSkillSwitch }] : []),
            ]}
            rows={rows}
            emptyText='No skills from this repository yet.'
          />
        )}
        {skipped.length > 0 && (
          <Box>
            <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-700)' }}>
              {`Not imported (${skipped.length})`}
            </Typography>
            <Box component='ul' sx={{ m: 0, mt: ds.space[1], pl: ds.space[4] }}>
              {skipped.map((sk) => (
                <Box component='li' key={`${sk.source_path}-${sk.reason}`} sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)' }}>
                  <Box component='span' sx={{ fontFamily: 'var(--ds-font-mono)' }}>
                    {sk.source_path || '(repository root)'}
                  </Box>
                  {` — ${sk.reason}`}
                </Box>
              ))}
            </Box>
          </Box>
        )}
      </Box>
    </Modal>
  );
};
SourceSkillsModal.propTypes = {
  source: PropTypes.object,
  skills: PropTypes.array.isRequired,
  onClose: PropTypes.func.isRequired,
  onOpenSkill: PropTypes.func.isRequired,
  renderSkillSwitch: PropTypes.func,
};

const SOURCE_MENU_ITEMS = [
  { label: 'Sync now', value: 'sync', icon: RefreshIcon },
  { label: 'Who can use it', value: 'visibility', icon: PeopleOutlineIcon },
  { label: 'Delete', value: 'delete', icon: deleteIcon },
];

/**
 * The organisation's skill sources, for tenant admins: list, add, sync now,
 * change who can use them, delete. `onChanged` refreshes the sources and the
 * knowledge-base list (a sync adds, updates or archives synced skills).
 */
const SkillSourcesSection = ({ sources = [], skills = [], loading = false, onChanged, renderSkillSwitch }) => {
  // The skills this view can list: the account's (or, tenant-wide, all), not
  // archived. The count and the skills modal both use it, so they agree; the
  // server's skill_count is the repository's total.
  const activeSkills = useMemo(() => skills.filter((kb) => kb.status !== 'archived'), [skills]);
  const listedFor = (sourceId) => activeSkills.filter((kb) => kb.source_id === sourceId);
  const [addOpen, setAddOpen] = useState(false);
  const [skillsSource, setSkillsSource] = useState(null);
  const [detailsSkill, setDetailsSkill] = useState(null);
  const [visibilitySource, setVisibilitySource] = useState(null);
  const [deleteSource, setDeleteSource] = useState(null);
  const [deleting, setDeleting] = useState(false);
  const [syncingId, setSyncingId] = useState(null);

  const handleSync = async (source) => {
    setSyncingId(source.id);
    try {
      const response = await apiKnowledgeBase.syncSkillSource(source.id);
      if (response.errors && response.errors.length > 0) {
        snackbar.error(response.errors[0]?.message || 'Failed to sync the repository');
        return;
      }
      const r = response.data || {};
      const skipped = r.skipped?.length || 0;
      snackbar.success(
        `Synced ${source.repo}${r.commit ? ` at ${shortCommit(r.commit)}` : ''}: ${r.found || 0} found, ${r.added || 0} added, ${
          r.updated || 0
        } updated, ${r.archived || 0} archived${skipped > 0 ? `, ${skipped} skipped` : ''}`
      );
    } catch (err) {
      console.error('Error syncing skill source:', err);
      snackbar.error('An error occurred while syncing the repository');
    } finally {
      setSyncingId(null);
      onChanged?.();
    }
  };

  const handleConfirmDelete = async () => {
    if (!deleteSource) return;
    setDeleting(true);
    try {
      const response = await apiKnowledgeBase.deleteSkillSource(deleteSource.id);
      if (response.errors && response.errors.length > 0) {
        snackbar.error(response.errors[0]?.message || 'Failed to remove the repository');
        return;
      }
      snackbar.success(`Removed ${deleteSource.repo} and its skills`);
      setDeleteSource(null);
      onChanged?.();
    } catch (err) {
      console.error('Error removing skill source:', err);
      snackbar.error('An error occurred while removing the repository');
    } finally {
      setDeleting(false);
    }
  };

  const columns = [
    {
      key: 'repo',
      label: 'Repository',
      type: 'text-long',
      render: (s) => (
        <Box sx={{ minWidth: 0 }}>
          <Link href={`https://github.com/${s.repo}`} openInNew>
            {s.repo}
          </Link>
          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
            {`${s.folder ? `/${s.folder}` : 'Whole repository'} · ${s.branch || 'default branch'}`}
          </Typography>
          <SkippedSkills skipped={s.sync_report?.skipped} />
        </Box>
      ),
    },
    {
      key: 'status',
      label: 'Status',
      type: 'status',
      render: (s) => {
        const status = SOURCE_STATUS[s.status] || { text: s.status, tone: 'neutral' };
        const label = <Label text={status.text} tone={status.tone} />;
        return s.error_message ? (
          <Tooltip title={s.error_message} placement='top'>
            <Box component='span' sx={{ display: 'inline-flex', cursor: 'help' }}>
              {label}
            </Box>
          </Tooltip>
        ) : (
          label
        );
      },
    },
    {
      key: 'last_synced_at',
      label: 'Last synced',
      type: 'text-short',
      render: (s) => (
        <Box>
          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)' }}>{formatSyncedAt(s.last_synced_at)}</Typography>
          {s.last_commit && (
            <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', fontFamily: 'var(--ds-font-mono)' }}>
              {shortCommit(s.last_commit)}
            </Typography>
          )}
        </Box>
      ),
    },
    {
      key: 'skill_count',
      label: 'Skills',
      type: 'count',
      render: (s) => {
        const listed = listedFor(s.id).length;
        // Fewer listed than the repository has: the rest aren't used by this account.
        const partial = listed < s.skill_count;
        return s.skill_count > 0 ? (
          <Button
            tone='link'
            size='sm'
            tooltip={partial ? 'Only the skills this account uses are listed' : 'View skills'}
            aria-label={`View ${listed} skills from ${s.repo}`}
            onClick={() => setSkillsSource(s)}
            data-testid='skill-source-skills-btn'
          >
            {partial ? `${listed} of ${s.skill_count}` : s.skill_count}
          </Button>
        ) : (
          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)' }}>0</Typography>
        );
      },
    },
    {
      key: 'visibility',
      label: 'Used by',
      type: 'label',
      render: (s) => (
        <Label
          text={
            s.visibility === SCOPE_SELECTED_ACCOUNTS
              ? `${(s.account_ids || []).length} account${(s.account_ids || []).length === 1 ? '' : 's'}`
              : 'Organisation'
          }
          tone='info'
        />
      ),
    },
    {
      key: 'actions',
      label: '',
      type: 'actions',
      render: (s) => (
        <Box sx={{ display: 'flex', justifyContent: 'flex-end' }} data-testid='skill-source-menu'>
          <ThreeDotsMenu
            menuItems={SOURCE_MENU_ITEMS.map((item) =>
              item.value === 'sync' ? { ...item, disabled: Boolean(syncingId) || s.status === 'syncing' } : item
            )}
            data={s}
            onMenuClick={(item) => {
              if (item.value === 'sync') handleSync(s);
              else if (item.value === 'visibility') setVisibilitySource(s);
              else if (item.value === 'delete') setDeleteSource(s);
            }}
            sx={{ p: ds.space[1], '& .MuiSvgIcon-root': { fontSize: 'var(--ds-text-title)' } }}
          />
        </Box>
      ),
    },
  ];

  return (
    <Box data-testid='skill-sources-section'>
      <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: ds.space[3] }}>
        <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)' }}>
          Skills synced from GitHub repositories. Each folder with a SKILL.md becomes an organisation skill; edit it in the repository.
        </Typography>
        <Button tone='secondary' size='sm' onClick={() => setAddOpen(true)} data-testid='skill-source-add-btn'>
          Add repository
        </Button>
      </Box>

      {!loading && sources.length === 0 ? (
        <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)', mt: ds.space[3] }}>No repositories yet.</Typography>
      ) : (
        <Box sx={{ mt: ds.space[3] }}>
          <MemoryTable columns={columns} rows={sources} emptyText='No repositories yet.' />
        </Box>
      )}

      <SourceSkillsModal
        source={skillsSource}
        skills={skillsSource ? listedFor(skillsSource.id) : []}
        onClose={() => setSkillsSource(null)}
        onOpenSkill={setDetailsSkill}
        renderSkillSwitch={renderSkillSwitch}
      />
      <SkillDetailsModal open={Boolean(detailsSkill)} onClose={() => setDetailsSkill(null)} knowledgeBase={detailsSkill} source={skillsSource} />
      <AddSkillSourceModal open={addOpen} onClose={() => setAddOpen(false)} onCreated={() => onChanged?.()} />
      <SkillSourceVisibilityModal source={visibilitySource} onClose={() => setVisibilitySource(null)} onSaved={() => onChanged?.()} />
      <Modal
        open={Boolean(deleteSource)}
        handleClose={() => !deleting && setDeleteSource(null)}
        title={`Delete repository: ${deleteSource?.repo || ''}`}
        width='sm'
        confirmText='Delete'
        confirmTone='danger'
        onConfirm={handleConfirmDelete}
        loader={deleting}
        backdropClickClose={!deleting}
      >
        <Typography sx={{ padding: ds.space[5], fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)' }}>
          {`Removes the repository and its ${pluralSkills(deleteSource?.skill_count || 0)}. Agents stop using them in every account.`}
        </Typography>
      </Modal>
    </Box>
  );
};
SkillSourcesSection.propTypes = {
  sources: PropTypes.array,
  skills: PropTypes.array,
  renderSkillSwitch: PropTypes.func,
  loading: PropTypes.bool,
  onChanged: PropTypes.func,
};

/**
 * Details of a skill synced from a skill source: where it came from, the files
 * of its folder, what it needs to run, and anything the sync warned about.
 */
export const SkillDetailsModal = ({ open, onClose, knowledgeBase, source }) => {
  const meta = knowledgeBase?.skill_meta || {};
  const requirements = meta.requirements || [];
  const warnings = meta.warnings || [];
  const files = meta.files || [];
  // Links into GitHub at the synced commit, so they show exactly what we stored.
  const repoUrl = source?.repo ? `https://github.com/${source.repo}` : '';
  const ref = meta.commit || source?.branch || 'HEAD';
  const githubPath = (kind, path) => (repoUrl ? `${repoUrl}/${kind}/${ref}/${path.split('/').map(encodeURIComponent).join('/')}` : '');
  const fileUrl = (f) => (f.skipped === 'unsafe path' ? '' : githubPath('blob', [meta.source_path, f.path].filter(Boolean).join('/')));
  const facts = [
    { label: 'Repository', value: source?.repo, href: repoUrl },
    { label: 'Path', value: meta.source_path, mono: true, href: meta.source_path ? githubPath('tree', meta.source_path) : '' },
    { label: 'Commit', value: shortCommit(meta.commit), mono: true, href: repoUrl && meta.commit ? `${repoUrl}/commit/${meta.commit}` : '' },
    { label: 'SKILL.md', value: repoUrl ? 'View on GitHub' : '', href: githubPath('blob', [meta.source_path, 'SKILL.md'].filter(Boolean).join('/')) },
    { label: 'License', value: meta.license },
    { label: 'Compatibility', value: meta.compatibility },
    { label: 'Allowed tools', value: meta.allowed_tools, mono: true },
  ].filter((f) => f.value);
  const sectionTitleSx = {
    fontSize: 'var(--ds-text-small)',
    fontWeight: 'var(--ds-font-weight-semibold)',
    color: 'var(--ds-gray-700)',
    mb: ds.space[2],
  };

  return (
    <Modal open={open} handleClose={onClose} title={`Skill: ${knowledgeBase?.name || ''}`} width='md'>
      <Box sx={{ padding: ds.space[5], display: 'flex', flexDirection: 'column', gap: ds.space[4] }} data-testid='skill-details-modal'>
        {warnings.length > 0 && <Banner tone='warning' message={warnings.join(' · ')} />}

        <Box sx={{ display: 'grid', gridTemplateColumns: 'max-content 1fr', columnGap: ds.space[4], rowGap: ds.space[1] }}>
          {facts.map((f) => [
            <Typography key={`${f.label}-l`} sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)' }}>
              {f.label}
            </Typography>,
            <Typography
              key={`${f.label}-v`}
              component='div'
              sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-700)', fontFamily: f.mono ? 'var(--ds-font-mono)' : undefined }}
            >
              {f.href ? (
                <Link href={f.href} openInNew>
                  {f.value}
                </Link>
              ) : (
                f.value
              )}
            </Typography>,
          ])}
        </Box>

        <Box>
          <Typography sx={sectionTitleSx}>Requirements</Typography>
          {requirements.length === 0 ? (
            <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)' }}>None declared.</Typography>
          ) : (
            <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: ds.space[2] }}>
              {requirements.map((req) => {
                // A note is the skill's free-text `compatibility` line, not something we can check.
                const isNote = req.kind === 'note';
                const missing = !isNote && req.provided === false;
                const text = isNote ? req.name : `${req.name} (${REQUIREMENT_KIND_LABELS[req.kind] || req.kind})`;
                return (
                  <Tooltip
                    key={`${req.kind}-${req.name}`}
                    title={isNote ? 'Compatibility note from the skill' : missing ? 'Not available to agents here' : 'Available to agents'}
                    placement='top'
                  >
                    <Box component='span' sx={{ display: 'inline-flex' }} data-testid='skill-requirement'>
                      <Label text={text} textTransform='none' tone={missing ? 'warning' : 'neutral'} />
                    </Box>
                  </Tooltip>
                );
              })}
            </Box>
          )}
        </Box>

        <Box>
          <Typography sx={sectionTitleSx}>Files</Typography>
          {files.length === 0 ? (
            <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)' }}>Only SKILL.md.</Typography>
          ) : (
            <MemoryTable
              columns={[
                {
                  key: 'path',
                  label: 'Path',
                  type: 'text-long',
                  render: (f) => (
                    <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], minWidth: 0 }} data-testid='skill-file'>
                      {fileUrl(f) ? (
                        <Link href={fileUrl(f)} openInNew>
                          {f.path}
                        </Link>
                      ) : (
                        <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-700)' }}>{f.path}</Typography>
                      )}
                      {/* Not stored, so agents can't read it; the skill still loads. */}
                      {f.skipped && <Label text={`Not stored: ${f.skipped}`} textTransform='none' tone='warning' />}
                    </Box>
                  ),
                },
                { key: 'size', label: 'Size', type: 'text-short', render: (f) => formatBytes(f.size) },
              ]}
              rows={files.map((f) => ({ ...f, id: f.path }))}
              emptyText='Only SKILL.md.'
            />
          )}
        </Box>
      </Box>
    </Modal>
  );
};
SkillDetailsModal.propTypes = {
  open: PropTypes.bool.isRequired,
  onClose: PropTypes.func.isRequired,
  knowledgeBase: PropTypes.object,
  source: PropTypes.object,
};

export default SkillSourcesSection;
