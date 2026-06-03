import React, { useEffect, useState, useCallback } from 'react';
import { useRouter } from 'next/router';
import { applyFiltersOnRouter } from '@lib/router';
import { Box } from '@mui/material';
import CloudProviderIcon from '@components1/common/CloudIcon';
import Chip from '@components1/ds/Chip';
import { Label } from '@components1/ds/Label';
import { Button as DsButton } from '@components1/ds/Button';
import { DropdownMenu as DsDropdownMenu } from '@components1/ds/DropdownMenu';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import DownloadButton from '@common-new/DownloadButton';
import CustomTable2 from '@common-new/tables/CustomTable2';
import { TriageRuleEventsTable } from '@components1/k8s/common/KubernetesTable2';
import Datetime from '@common-new/format/Datetime';
import EmptyData from '@components1/common/EmptyData';
import { DataNotAvailable } from '@assets';
import MoreVertIcon from '@mui/icons-material/MoreVert';
import NDialog from '@components1/common/modal/NDialog';
import Text from '@common-new/format/Text';
import { toast as snackbar } from '@components1/ds/Toast';
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
  const [totalCount, setTotalCount] = useState(0);
  const [loading, setLoading] = useState(false);

  // Pagination state
  const [currentPage, setCurrentPage] = useState(0);
  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());

  // Filter state
  const [selectedRuleType, setSelectedRuleType] = useState<string>('');
  const [selectedStatus, setSelectedStatus] = useState<string>('');
  const [selectedAccountFilter, setSelectedAccountFilter] = useState<string[]>(() => {
    const raw = router.query.accountId as string;
    return raw ? raw.split(',').filter(Boolean) : [];
  });

  useEffect(() => {
    const raw = router.query.accountId as string;
    setSelectedAccountFilter(raw ? raw.split(',').filter(Boolean) : []);
  }, [router.query.accountId]);

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

  const handleToggleSystemRuleOverride = async (rule: TriageRule) => {
    if (!accountId) {
      snackbar.error('Account ID is required to toggle system rule');
      return;
    }
    try {
      const newDisabledState = !rule.is_overridden;
      const result = await apiTriage.toggleSystemRuleOverride({
        cloud_account_id: accountId,
        system_rule_id: rule.id,
        disabled: newDisabledState,
      });

      if (result?.success) {
        snackbar.success(
          newDisabledState
            ? `System rule "${rule.name || 'Unnamed'}" disabled for this account`
            : `System rule "${rule.name || 'Unnamed'}" enabled for this account`
        );
        fetchRules();
      } else {
        snackbar.error(result?.error || 'Failed to toggle system rule');
      }
    } catch (error) {
      console.error('Failed to toggle system rule override:', error);
      snackbar.error('Failed to toggle system rule');
    }
  };

  const fetchRules = useCallback(async () => {
    setLoading(true);
    try {
      const enabled = selectedStatus === 'Enabled' ? true : selectedStatus === 'Disabled' ? false : undefined;
      // Use selectedAccountFilter when in multi-account view, otherwise use accountId prop
      const result = await apiTriage.getTriageRules({
        cloud_account_id: !isMultiAccountView ? accountId : undefined,
        cloud_account_ids: isMultiAccountView && selectedAccountFilter.length ? selectedAccountFilter : undefined,
        rule_type: selectedRuleType || undefined,
        enabled,
      });

      const rulesData = result?.rules || [];
      setRules(rulesData);
      setTotalCount(rulesData.length);
    } catch (error) {
      console.error('Failed to fetch triage rules:', error);
      snackbar.error('Failed to fetch triage rules');
    } finally {
      setLoading(false);
    }
  }, [accountId, selectedRuleType, selectedStatus, isMultiAccountView, selectedAccountFilter]);

  useEffect(() => {
    fetchRules();
  }, [fetchRules]);

  // Transform rules to table data
  useEffect(() => {
    const startIdx = currentPage * rowsPerPage;
    const endIdx = startIdx + rowsPerPage;
    const paginatedRules = rules.slice(startIdx, endIdx);

    const data = paginatedRules.map((rule) => {
      const matchCriteria = getMatchCriteriaSummary(rule);
      const actionDisplay = getActionDisplay(rule);

      // Build drilldown query for expandable row
      const drilldownQuery = buildDrilldownQuery(rule);

      // Account cell for multi-account view
      const accountCell: any[] = [];
      if (isMultiAccountView) {
        const account = accounts.find((acc) => (acc.id || acc.value) === rule.account_id);
        accountCell.push({
          component: <Text showAutoEllipsis value={account?.label || account?.account_name || rule.account_id} />,
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
  }, [rules, currentPage, rowsPerPage, accountId, isMultiAccountView, accounts]);

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
    try {
      // For now, we use delete with hard_delete=false to disable
      // A proper enable/disable endpoint would be better
      if (rule.enabled) {
        await apiTriage.deleteTriageRule({ cloud_account_id: ruleAccountId, rule_id: rule.id, hard_delete: false });
        snackbar.success(`Rule "${rule.name || 'Unnamed'}" disabled`);
      } else {
        // Re-enabling would require a separate API endpoint
        snackbar.info('To re-enable a rule, please create a new one');
      }
      fetchRules();
    } catch (error) {
      console.error('Failed to toggle rule:', error);
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

  const handleRuleSuccess = () => {
    handleCloseRuleModal();
    fetchRules();
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
                applyFiltersOnRouter(router, { accountId: ids.join(',') });
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
        </ListingLayout.Toolbar>

        <ListingLayout.Body>
          {!loading && rules.length === 0 ? (
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
