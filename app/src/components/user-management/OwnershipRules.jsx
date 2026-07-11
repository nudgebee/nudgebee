import React, { useCallback, useEffect, useState } from 'react';
import { Box } from '@mui/material';
import { ListingLayout } from '@ui/ListingLayout';
import CustomTable from '@shared/tables/CustomTable2';
import { Button as DsButton } from '@ui/Button';
import { Chip } from '@ui/Chip';
import { Switch } from '@ui/Switch';
import { Modal } from '@ui/Modal';
import Tooltip from '@ui/Tooltip';
import Text from '@shared/format/Text';
import { toast as snackbar } from '@ui/Toast';
import { isTenantAdmin } from '@lib/auth';
import ThreeDotsMenu from '@ui/ThreeDotsMenu';
import apiOwnership from '@api1/ownership';
import apiUserManagement from '@api1/user';
import useOwnerDirectory from '@components/ownership/useOwnerDirectory';
import OwnershipRuleModal from '@components/ownership/OwnershipRuleModal';

const RULE_MENU_ITEMS = [
  { id: 'edit', label: 'Edit' },
  { id: 'delete', label: 'Delete' },
];

// Ownership rules management — a tab on /user-management. Lists ownership_rules,
// each mapping a workload label/namespace to a user/group owner. Rules are
// evaluated lazily server-side, so a change here reflects on the workloads Owner
// column on the next resolve (no sync). Tenant-admin only for writes.
const HEADERS = [{ name: 'Name' }, { name: 'Match' }, { name: 'Account scope' }, { name: 'Owner' }, { name: 'Enabled' }, ''];

export default function OwnershipRules() {
  const canWrite = isTenantAdmin();
  const { ownerLabel } = useOwnerDirectory();
  const [rules, setRules] = useState([]);
  const [accounts, setAccounts] = useState({});
  const [loading, setLoading] = useState(true);
  const [modalOpen, setModalOpen] = useState(false);
  const [editingRule, setEditingRule] = useState(null);
  const [deleting, setDeleting] = useState(null);

  const fetchRules = useCallback(() => {
    setLoading(true);
    apiOwnership
      .listRules()
      .then((rows) => setRules(Array.isArray(rows) ? rows : []))
      .catch(() => setRules([]))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    fetchRules();
    apiUserManagement
      .listAccounts()
      .then((rows) => {
        const map = {};
        (Array.isArray(rows) ? rows : []).forEach((a) => {
          map[a.id] = a.account_name || a.id;
        });
        setAccounts(map);
      })
      .catch(() => setAccounts({}));
  }, [fetchRules]);

  const openAdd = () => {
    setEditingRule(null);
    setModalOpen(true);
  };
  const openEdit = (rule) => {
    setEditingRule(rule);
    setModalOpen(true);
  };
  const handleModalClose = (changed) => {
    setModalOpen(false);
    if (changed) fetchRules();
  };

  const confirmDelete = async () => {
    if (!deleting) return;
    try {
      await apiOwnership.deleteRule(deleting.id);
      snackbar.success('Rule deleted');
      fetchRules();
    } catch {
      snackbar.error('Failed to delete rule');
    } finally {
      setDeleting(null);
    }
  };

  // Inline enable/disable from the list — optimistic, reverts on failure. Re-sends
  // the whole rule (upsert) with the flipped flag.
  const toggleEnabled = async (r, next) => {
    setRules((prev) => prev.map((x) => (x.id === r.id ? { ...x, enabled: next } : x)));
    try {
      await apiOwnership.upsertRule({
        id: r.id,
        name: r.name,
        resourceDomain: r.resource_domain || 'k8s',
        matchScope: r.match_scope,
        matchKey: r.match_key || '',
        matchValue: r.match_value,
        cloudAccountId: r.cloud_account_id || '',
        ownerType: r.owner_type,
        ownerId: r.owner_id,
        enabled: next,
      });
      snackbar.success(next ? 'Rule enabled' : 'Rule disabled');
    } catch {
      snackbar.error('Failed to update rule');
      setRules((prev) => prev.map((x) => (x.id === r.id ? { ...x, enabled: !next } : x)));
    }
  };

  // Scannable Match cell: a scope chip + readable detail. Workload rules show the
  // namespace + a count, with the full name list in a tooltip (the list gets long).
  const matchCell = (r) => {
    let scope = 'Label';
    let detail = `${r.match_key} = ${r.match_value}`;
    let tooltip = null;
    if (r.match_scope === 'namespace') {
      scope = 'Namespace';
      detail = r.match_value;
    } else if (r.match_scope === 'workload') {
      scope = 'Workloads';
      const names = (r.match_key || '')
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean);
      detail = `${r.match_value} · ${names.length} workload${names.length === 1 ? '' : 's'}`;
      tooltip = names.join(', ');
    } else if (r.match_scope === 'cloud_tag') {
      scope = 'Tag';
      detail = `${r.match_key} = ${r.match_value}`;
    } else if (r.match_scope === 'cloud_type') {
      scope = 'Type';
      detail = r.match_value;
    } else if (r.match_scope === 'cloud_region') {
      scope = 'Region';
      detail = r.match_value;
    } else if (r.match_scope === 'cloud_resource') {
      scope = 'Resources';
      const ids = (r.match_key || '')
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean);
      detail = `${ids.length} resource${ids.length === 1 ? '' : 's'}`;
      tooltip = ids.join(', ');
    }
    const detailNode = <Text value={detail} showAutoEllipsis />;
    return (
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.75, minWidth: 0 }}>
        <Chip variant='tag' size='2xs' tone='neutral'>
          {scope}
        </Chip>
        <Box sx={{ minWidth: 0 }}>
          {tooltip ? (
            <Tooltip title={tooltip} arrow>
              {<span>{detailNode}</span>}
            </Tooltip>
          ) : (
            detailNode
          )}
        </Box>
      </Box>
    );
  };

  const tableData = rules.map((r) => [
    { component: <Text value={r.name} showAutoEllipsis /> },
    { component: matchCell(r) },
    { component: <Text value={r.cloud_account_id ? accounts[r.cloud_account_id] || r.cloud_account_id : 'All accounts'} showAutoEllipsis /> },
    {
      component: (
        <Chip variant='tag' size='xs' tone={r.owner_type === 'group' ? 'info' : 'neutral'}>
          {ownerLabel(r.owner_type, r.owner_id)}
        </Chip>
      ),
    },
    {
      component: canWrite ? (
        <Switch checked={!!r.enabled} onChange={(_e, next) => toggleEnabled(r, next)} aria-label={`Toggle ${r.name}`} />
      ) : (
        <Chip variant='status' size='xs' tone={r.enabled ? 'success' : 'neutral'}>
          {r.enabled ? 'Enabled' : 'Disabled'}
        </Chip>
      ),
    },
    {
      component: (
        <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'flex-end' }}>
          <ThreeDotsMenu
            id={`rule-actions-${r.id}`}
            data={r}
            menuItems={canWrite ? RULE_MENU_ITEMS : []}
            onMenuClick={(item, rule) => {
              if (item.id === 'edit') openEdit(rule);
              else if (item.id === 'delete') setDeleting(rule);
            }}
          />
        </Box>
      ),
    },
  ]);

  return (
    <ListingLayout id='ownership-rules'>
      <ListingLayout.Toolbar
        title='Ownership rules'
        actions={
          canWrite ? (
            <DsButton id='add-rule' tone='primary' size='md' onClick={openAdd}>
              Add rule
            </DsButton>
          ) : undefined
        }
      />
      <ListingLayout.Body>
        <CustomTable tableData={tableData} headers={HEADERS} loading={loading} id='ownership-rules-table' />
      </ListingLayout.Body>
      {modalOpen ? <OwnershipRuleModal open={modalOpen} onClose={handleModalClose} rule={editingRule} /> : null}
      <Modal open={!!deleting} handleClose={() => setDeleting(null)} title='Delete rule' width='xs' confirmText='Delete' onConfirm={confirmDelete}>
        <Text value={deleting ? `Delete rule "${deleting.name}"? This cannot be undone.` : ''} />
      </Modal>
    </ListingLayout>
  );
}
