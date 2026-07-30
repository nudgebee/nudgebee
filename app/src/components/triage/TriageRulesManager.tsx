import React, { useEffect, useState, useCallback, useMemo } from 'react';
import { useRouter } from 'next/router';
import { applyFiltersOnRouter } from '@lib/router';
import { Box } from '@mui/material';
import CloudProviderIcon from '@shared/icons/CloudIcon';
import Chip from '@ui/Chip';
import { Label } from '@ui/Label';
import { Button as DsButton } from '@ui/Button';
import { DropdownMenu as DsDropdownMenu } from '@ui/DropdownMenu';
import { ListingLayout } from '@ui/ListingLayout';
import FilterDropdown from '@ui/FilterDropdown';
import { Switch } from '@ui/Switch';
import DownloadButton from '@shared/buttons/DownloadButton';
import CustomTable2 from '@shared/tables/CustomTable2';
import { TriageRuleEventsTable } from '@components/k8s/common/KubernetesTable2';
import Datetime from '@shared/format/Datetime';
import EmptyData from '@shared/EmptyData';
import { DataNotAvailable } from '@assets';
import MoreVertIcon from '@mui/icons-material/MoreVert';
import NDialog from '@shared/modal/NDialog';
import Text from '@shared/format/Text';
import { toast as snackbar } from '@ui/Toast';
import { hasWriteAccess } from '@lib/auth';
import apiUser from '@api1/user';
import apiTriage, { type TriageRule } from '@api1/triage';
import TriageRuleModal from './TriageRuleModal';
import useKubernetesEventFilters from '@hooks/useKubernetesEventFilters';

interface TriageRulesManagerProps {
  accountId?: string;
}

interface AccountOption {
  id?: string;
  value?: string;
  label?: string;
  account_name?: string;
  cloud_provider?: string;
}

const RULE_TYPE_OPTIONS = [
  { label: 'Suppression', value: 'suppression' },
  { label: 'Scoring', value: 'scoring' },
  { label: 'Classification', value: 'classification' },
];

const STATUS_OPTIONS = ['Enabled', 'Disabled'];

const renderAccountGroupIcon = (provider: string) => <CloudProviderIcon cloud_provider={provider} width='14px' height='14px' />;

const TriageRulesManager: React.FC<TriageRulesManagerProps> = ({ accountId }) => {
  const router = useRouter();
  const tableId = 'triageRulesManager';
  const isMultiAccountView = !accountId;

  // Get accounts list for multi-account view
  const { accounts } = useKubernetesEventFilters({
    selectedAccountId: accountId,
    isTroubleshootPage: isMultiAccountView,
    enableFilters: isMultiAccountView,
    disabledFilters: ['workload', 'namespace', 'subjectType', 'aggregationKey', 'source'],
    resource_ids: [],
  }) as { accounts: AccountOption[] };

  // Data state
  const [rules, setRules] = useState<TriageRule[]>([]);
  const [tableData, setTableData] = useState<any[]>([]);
  const [loading, setLoading] = useState(false);

  // Pagination state
  const [currentPage, setCurrentPage] = useState(0);
  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());

  // Filter state
  const [selectedRuleType, setSelectedRuleType] = useState<string>('');
  const [selectedStatus, setSelectedStatus] = useState<string>('');
  const [includeSystemRules, setIncludeSystemRules] = useState(true);
  const [selectedAccountFilter, setSelectedAccountFilter] = useState<string[]>(() => {
    const raw = router.query.accountIds as string;
    return raw ? raw.split(',').filter(Boolean) : [];
  });

  // System-rule visibility and Status are both filtered client-side. Status uses each
  // rule's *effective* status (a system rule disabled for this account via an override
  // keeps enabled=true), matching the badge in getStatusDisplay — filtering on the raw
  // `enabled` column alone would mismatch override-disabled system rules.
  const filteredRules = useMemo(() => {
    let out = includeSystemRules ? rules : rules.filter((r) => !r.is_system_rule);
    if (selectedStatus === 'Enabled' || selectedStatus === 'Disabled') {
      const wantEnabled = selectedStatus === 'Enabled';
      out = out.filter((r) => {
        const effectiveEnabled = r.enabled && !(r.is_system_rule && r.is_overridden);
        return effectiveEnabled === wantEnabled;
      });
    }
    return out;
  }, [rules, includeSystemRules, selectedStatus]);
  const totalCount = filteredRules.length;

  useEffect(() => {
    const raw = router.query.accountIds as string;
    setSelectedAccountFilter(raw ? raw.split(',').filter(Boolean) : []);
  }, [router.query.accountIds]);

  // Modal state
  const [isRuleModalOpen, setIsRuleModalOpen] = useState(false);
  const [selectedRule, setSelectedRule] = useState<TriageRule | null>(null);
  const [isCreateMode, setIsCreateMode] = useState(true);

  // Delete confirmation state
  const [deleteDialogOpen, setDeleteDialogOpen] = useState(false);
  const [ruleToDelete, setRuleToDelete] = useState<TriageRule | null>(null);

  // Build query object for the triage rule drilldown using the new indexed API
  const buildDrilldownQuery = (rule: TriageRule): Record<string, any> => {
    const query: Record<string, any> = {};

    // Set the account ID for the events query
    query.accountId = rule.account_id || accountId;

    // Pass the rule ID to use the fast indexed query via event_classification table
    query.triageRuleId = rule.id;

    // Set time range to last 30 days (now performant with indexed query)
    const now = new Date();
    query.startDate = new Date(now.getTime() - 30 * 24 * 60 * 60 * 1000);
    query.endDate = now;

    return query;
  };

  const getMenuItems = (item: TriageRule) => {
    const menus: Array<{ label: string; id: number }> = [];

    // System rules have special handling
    if (item.is_system_rule) {
      // For system rules, only show toggle option if user has write access to the account
      if (accountId && hasWriteAccess(accountId)) {
        menus.push({
          label: item.is_overridden ? 'Enable System Rule' : 'Disable for this Account',
          id: 4,
        });
      }
      return menus;
    }

    // Use rule's account_id - each rule knows which account it belongs to
    const ruleAccountId = item.account_id;

    if (!ruleAccountId || !hasWriteAccess(ruleAccountId)) {
      return menus;
    }

    if (item.is_editable) {
      menus.push({
        label: 'Edit',
        id: 1,
      });
      menus.push({
        label: item.enabled ? 'Disable' : 'Enable',
        id: 2,
      });
      menus.push({
        label: 'Delete',
        id: 3,
      });
    }

    return menus;
  };

  const onMenuClick = (menuItem: { id: number; label: string }, data: TriageRule) => {
    if (menuItem.id === 1) {
      // Edit
      setSelectedRule(data);
      setIsCreateMode(false);
      setIsRuleModalOpen(true);
    } else if (menuItem.id === 2) {
      // Enable/Disable
      handleToggleRule(data);
    } else if (menuItem.id === 3) {
      // Delete
      setRuleToDelete(data);
      setDeleteDialogOpen(true);
    } else if (menuItem.id === 4) {
      // Toggle system rule override
      handleToggleSystemRuleOverride(data);
    }
  };

  const renderActionMenu = (rule: TriageRule) => {
    const items = getMenuItems(rule);
    if (items.length === 0) {
      return null;
    }
    return (
      <DsDropdownMenu
        align='end'
        size='sm'
        items={items.map((m) => ({
          id: `triage-action-${rule.id}-${m.id}`,
          label: m.label,
          onSelect: () => onMenuClick({ id: m.id, label: m.label }, rule),
        }))}
        trigger={<DsButton tone='ghost' size='xs' composition='icon-only' aria-label='More actions' icon={<MoreVertIcon />} />}
      />
    );
  };

  // Update a single rule in local state without refetching the whole list.
  const patchRule = (ruleId: string, patch: Partial<TriageRule>) => {
    setRules((prev) => prev.map((r) => (r.id === ruleId ? { ...r, ...patch } : r)));
  };

  const handleToggleSystemRuleOverride = async (rule: TriageRule) => {
    if (!accountId) {
      snackbar.error('Account ID is required to toggle system rule');
      return;
    }
    const previousOverriddenState = rule.is_overridden;
    const newDisabledState = !previousOverriddenState;
    patchRule(rule.id, { is_overridden: newDisabledState }); // optimistic update

    try {
      const result = await apiTriage.toggleSystemRuleOverride({
        cloud_account_id: accountId,
        system_rule_id: rule.id,
        disabled: newDisabledState,
      });

      if (result?.success) {
        // optimistic update already applied — no snackbar needed
        console.log(`System rule "${rule.name || 'Unnamed'}" override toggled to ${newDisabledState ? 'disabled' : 'enabled'}`);
      } else {
        patchRule(rule.id, { is_overridden: previousOverriddenState }); // revert on failure response
        snackbar.error(result?.error || 'Failed to toggle system rule');
      }
    } catch (error) {
      console.error('Failed to toggle system rule override:', error);
      patchRule(rule.id, { is_overridden: previousOverriddenState }); // revert on request error
      snackbar.error('Failed to toggle system rule');
    }
  };

  const fetchRules = useCallback(async () => {
    setLoading(true);
    try {
      // Status is filtered client-side (see filteredRules): a system rule disabled
      // for this account keeps enabled=true and is only disabled via a per-account
      // override, so a server-side `enabled` filter would drop those rows before the
      // client can classify them by their effective (displayed) status.
      const result = await apiTriage.getTriageRules({
        cloud_account_id: !isMultiAccountView ? accountId : undefined,
        cloud_account_ids: isMultiAccountView && selectedAccountFilter.length ? selectedAccountFilter : undefined,
        rule_type: selectedRuleType || undefined,
      });

      const rulesData = result?.rules || [];
      setRules(rulesData);
    } catch (error) {
      console.error('Failed to fetch triage rules:', error);
      snackbar.error('Failed to fetch triage rules');
    } finally {
      setLoading(false);
    }
  }, [accountId, selectedRuleType, isMultiAccountView, selectedAccountFilter]);

  useEffect(() => {
    fetchRules();
  }, [fetchRules]);

  // Transform rules to table data
  useEffect(() => {
    const startIdx = currentPage * rowsPerPage;
    const endIdx = startIdx + rowsPerPage;
    const paginatedRules = filteredRules.slice(startIdx, endIdx);

    const data = paginatedRules.map((rule) => {
      const matchCriteria = getMatchCriteriaSummary(rule);
      const actionDisplay = getActionDisplay(rule);

      // Build drilldown query for expandable row
      const drilldownQuery = buildDrilldownQuery(rule);

      // Account cell for multi-account view
      const accountCell: any[] = [];
      if (isMultiAccountView) {
        const account = accounts.find((acc) => (acc.id || acc.value) === rule.account_id);
        const accountName = rule.is_system_rule && !rule.account_id ? 'All Accounts' : account?.label || account?.account_name || rule.account_id;
        accountCell.push({
          component: <Text showAutoEllipsis value={accountName} />,
          drilldownQuery,
        });
      }

      // Determine status display for system rules
      const getStatusDisplay = () => {
        if (rule.is_system_rule && rule.is_overridden) {
          return <Label text='Disabled (Override)' variant='grey' dot />;
        }
        return <Label text={rule.enabled ? 'Enabled' : 'Disabled'} variant={rule.enabled ? 'green' : 'grey'} dot />;
      };

      return [
        ...accountCell,
        {
          component: (
            <Box display='flex' flexDirection='column' alignItems='flex-start' gap='var(--ds-space-1)'>
              <Text value={rule.name || 'Unnamed Rule'} />
              {rule.is_system_rule && (
                <Chip variant='tag' size='xs' tone='info'>
                  System
                </Chip>
              )}
            </Box>
          ),
          drilldownQuery,
        },
        {
          component: <Label text={getRuleTypeLabel(rule.rule_type)} variant='grey' />,
        },
        { component: <Text value={actionDisplay} /> },
        { component: <Text value={matchCriteria || '-'} /> },
        {
          component: (
            <Text
              value={rule.priority.toString()}
              sx={{
                fontSize: 'var(--ds-text-caption)',
                textAlign: 'center',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
              }}
            />
          ),
        },
        { component: getStatusDisplay() },
        {
          component: (
            <Text
              value={rule.match_count.toString()}
              sx={{
                fontSize: 'var(--ds-text-caption)',
                textAlign: 'center',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
              }}
            />
          ),
        },
        { component: <Datetime value={rule.created_at} /> },
        { component: renderActionMenu(rule) },
      ];
    });

    setTableData(data);
  }, [filteredRules, currentPage, rowsPerPage, accountId, isMultiAccountView, accounts]);

  const getMatchCriteriaSummary = (rule: TriageRule): string => {
    const criteria: string[] = [];

    // Handle occurrence-based matching (for system duplicate rule)
    if (rule.match_occurrence_greater_than !== undefined && rule.match_occurrence_greater_than !== null) {
      criteria.push(`Occurrence > ${rule.match_occurrence_greater_than}`);
    }
    if (rule.match_fingerprint) {
      criteria.push(`Fingerprint: ${rule.match_fingerprint.substring(0, 20)}...`);
    }
    if (rule.match_alertname) {
      criteria.push(`Alert: ${rule.match_alertname}`);
    }
    if (rule.match_namespace) {
      criteria.push(`NS: ${rule.match_namespace}`);
    }
    if (rule.match_service) {
      criteria.push(`Svc: ${rule.match_service}`);
    }
    if (rule.match_source) {
      criteria.push(`Source: ${rule.match_source}`);
    }

    return criteria.length > 0 ? criteria.slice(0, 2).join(', ') : 'Any';
  };

  const getActionDisplay = (rule: TriageRule): string => {
    if (rule.rule_type === 'suppression') {
      return rule.action === 'drop' ? 'Drop Events' : 'Suppress Events';
    } else if (rule.rule_type === 'scoring') {
      return `Adjust Score: ${rule.action_value || 'N/A'}`;
    } else if (rule.rule_type === 'classification') {
      return `Auto-classify: ${rule.action}`;
    }
    return rule.action;
  };

  const getRuleTypeLabel = (ruleType: string): string => {
    const option = RULE_TYPE_OPTIONS.find((o) => o.value === ruleType);
    return option?.label || ruleType;
  };

  const handleToggleRule = async (rule: TriageRule) => {
    const ruleAccountId = rule.account_id;
    if (!ruleAccountId) {
      snackbar.error('Cannot modify rule without account ID');
      return;
    }
    if (!rule.enabled) {
      // Re-enabling would require a separate API endpoint
      snackbar.info('To re-enable a rule, please create a new one');
      return;
    }

    // For now, we use delete with hard_delete=false to disable
    // A proper enable/disable endpoint would be better
    patchRule(rule.id, { enabled: false }); // optimistic update

    try {
      const result = await apiTriage.deleteTriageRule({ cloud_account_id: ruleAccountId, rule_id: rule.id, hard_delete: false });
      if (result?.success) {
        // optimistic update already applied — no snackbar needed
        console.log(`Rule "${rule.name || 'Unnamed'}" disabled`);
      } else {
        patchRule(rule.id, { enabled: true }); // revert
        snackbar.error(result?.error || 'Failed to update rule');
      }
    } catch (error) {
      console.error('Failed to toggle rule:', error);
      patchRule(rule.id, { enabled: true }); // revert
      snackbar.error('Failed to update rule');
    }
  };

  const handleDeleteRule = async () => {
    if (!ruleToDelete) {
      return;
    }

    const ruleAccountId = ruleToDelete.account_id;
    if (!ruleAccountId) {
      snackbar.error('Cannot delete rule without account ID');
      setDeleteDialogOpen(false);
      setRuleToDelete(null);
      return;
    }

    try {
      const result = await apiTriage.deleteTriageRule({
        cloud_account_id: ruleAccountId,
        rule_id: ruleToDelete.id,
        hard_delete: true,
      });

      if (result?.success) {
        snackbar.success(`Rule "${ruleToDelete.name || 'Unnamed'}" deleted`);
        fetchRules();
      } else {
        snackbar.error(result?.error || 'Failed to delete rule');
      }
    } catch (error) {
      console.error('Failed to delete rule:', error);
      snackbar.error('Failed to delete rule');
    } finally {
      setDeleteDialogOpen(false);
      setRuleToDelete(null);
    }
  };

  const handleCloseDeleteDialog = () => {
    setDeleteDialogOpen(false);
    setRuleToDelete(null);
  };

  const handleOpenCreateModal = () => {
    setSelectedRule(null);
    setIsCreateMode(true);
    setIsRuleModalOpen(true);
  };

  const handleCloseRuleModal = () => {
    setIsRuleModalOpen(false);
    setSelectedRule(null);
    setIsCreateMode(true);
  };

  const handleRuleSuccess = (savedRule?: TriageRule) => {
    const wasCreate = isCreateMode;
    handleCloseRuleModal();

    if (wasCreate || !savedRule) {
      // New rows need server-assigned fields (is_editable, account_id, match_count, ...)
      // that the mutation response doesn't include, so refetch the list.
      fetchRules();
      return;
    }

    // Edits return the full updated rule - patch it in directly, no refetch needed.
    patchRule(savedRule.id, savedRule);
  };

  const onPageChange = (page: number, limit: number) => {
    setCurrentPage(page - 1);
    setRowsPerPage(limit);
  };

  return (
    <div>
      <NDialog
        buttonText='Delete'
        handleClose={handleCloseDeleteDialog}
        dialogTitle={`Delete rule "${ruleToDelete?.name || 'Unnamed'}"`}
        handleSubmit={handleDeleteRule}
        open={deleteDialogOpen}
        dialogContent='This action cannot be undone. The rule will be permanently deleted.'
        additionalComponent={undefined}
      />

      <TriageRuleModal
        open={isRuleModalOpen}
        handleClose={handleCloseRuleModal}
        accountId={selectedRule?.account_id || accountId}
        rule={selectedRule}
        isCreate={isCreateMode}
        onSuccess={handleRuleSuccess}
      />

      <ListingLayout id='triage-rules-list-box'>
        <ListingLayout.Toolbar
          data-testid='triage-rules-filter-toolbar'
          actions={
            <>
              {accountId && hasWriteAccess(accountId) && (
                <DsButton tone='primary' size='md' onClick={handleOpenCreateModal} id='create-rule-btn'>
                  Create Rule
                </DsButton>
              )}
              <DownloadButton id={`${tableId}-download`} onClick={() => ({ tableId })} />
            </>
          }
        >
          {isMultiAccountView && (
            <FilterDropdown
              id='triage-rules-filter-account'
              label='Account'
              multiple
              grouped
              groupIcon={renderAccountGroupIcon}
              options={accounts.map((acc: AccountOption) => ({
                label: acc.label || acc.account_name || acc.id,
                value: acc.id || acc.value,
                group: acc.cloud_provider || 'Other',
              }))}
              value={accounts
                .filter((acc: AccountOption) => selectedAccountFilter.includes((acc.id || acc.value) as string))
                .map((acc: AccountOption) => ({
                  label: acc.label || acc.account_name || acc.id,
                  value: acc.id || acc.value,
                  group: acc.cloud_provider || 'Other',
                }))}
              onSelect={(_e: any, items: any) => {
                const ids = (Array.isArray(items) ? items : []).map((v: any) => v.value);
                setSelectedAccountFilter(ids);
                setCurrentPage(0);
                applyFiltersOnRouter(router, { accountIds: ids.join(',') });
              }}
            />
          )}
          <FilterDropdown
            id='triage-rules-filter-rule-type'
            label='Rule Type'
            options={RULE_TYPE_OPTIONS}
            value={RULE_TYPE_OPTIONS.find((o) => o.value === selectedRuleType) ?? null}
            onSelect={(_e: any, item: any) => {
              setSelectedRuleType(item?.value || '');
              setCurrentPage(0);
            }}
          />
          <FilterDropdown
            id='triage-rules-filter-status'
            label='Status'
            options={STATUS_OPTIONS.map((s) => ({ label: s, value: s }))}
            value={selectedStatus ? { label: selectedStatus, value: selectedStatus } : null}
            onSelect={(_e: any, item: any) => {
              setSelectedStatus(item?.value || '');
              setCurrentPage(0);
            }}
          />
          <Switch
            id='triage-rules-filter-include-system'
            label='System Rules'
            size='sm'
            checked={includeSystemRules}
            onChange={(_e, checked) => {
              setIncludeSystemRules(checked);
              setCurrentPage(0);
            }}
          />
        </ListingLayout.Toolbar>

        <ListingLayout.Body>
          {!loading && filteredRules.length === 0 ? (
            <EmptyData
              id='triage-rules-empty'
              img={DataNotAvailable}
              heading='No Data Available'
              subHeading='Create rules to automatically suppress, score, or classify events'
            />
          ) : (
            <CustomTable2
              id={tableId}
              totalRows={totalCount}
              tableData={tableData}
              headers={[
                ...(isMultiAccountView ? [{ name: 'Account Name', width: '12%' }] : []),
                { name: 'Name', width: '18%' },
                { name: 'Type', width: '10%' },
                { name: 'Action', width: '12%' },
                { name: 'Match Criteria', width: '15%' },
                { name: 'Priority', width: '8%' },
                { name: 'Status', width: '10%' },
                { name: 'Matches', width: '8%' },
                { name: 'Created', width: '10%' },
                { name: '', width: '5%' },
              ]}
              rowsPerPage={rowsPerPage}
              showExpandable
              expandable={{
                tabs: [
                  {
                    text: 'Matched Events',
                    value: 0,
                    key: 'triage-rule-events',
                    componentFn: (_option: any, drilldownQuery: any) => <TriageRuleEventsTable query={drilldownQuery} />,
                  },
                ],
              }}
              loading={loading}
              onPageChange={onPageChange}
              onSortChange={undefined}
              pageNumber={currentPage + 1}
              tableHeadingCenter={['Type', 'Priority', 'Status', 'Matches']}
            />
          )}
        </ListingLayout.Body>
      </ListingLayout>
    </div>
  );
};

export default TriageRulesManager;
