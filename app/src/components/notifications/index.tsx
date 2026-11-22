import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { Box, Typography } from '@mui/material';
import SearchIcon from '@mui/icons-material/Search';
import { ExpandMore, MoreVert } from '@mui/icons-material';
import { ListingLayout } from '@ui/ListingLayout';
import { Button as DsButton } from '@ui/Button';
import { Chip } from '@ui/Chip';
import { Input } from '@ui/Input';
import { Callout } from '@ui/Callout';
import { DropdownMenu } from '@ui/DropdownMenu';
import Tooltip from '@ui/Tooltip';
import { toast } from '@ui/Toast';
import CustomTable from '@shared/tables/CustomTable';
import Datetime from '@shared/format/Datetime';
import Text from '@shared/format/Text';
import SafeIcon from '@shared/icons/SafeIcon';
import { DeleteIconRed, EditIcon } from '@assets';
import apiNotifications from '@api1/notification';
import apiDashboard from '@api1/home';
import apiUser from '@api1/user';
import { canManage } from '@lib/auth';
import useAdminAction from '@components/user-management/useAdminAction';
import { ds } from 'src/utils/colors';
import DeleteNotificationRuleModal from './DeleteNotificationRuleModal';
import { RULE_TYPE, RULE_TYPES, RuleTypeKey, isRuleTypeKey } from './rules/catalogue';
import { Rule, RuleIssue, newRule, ruleFromRow, ruleIssues, toUpsertInput, typesOf, whatText, whoText } from './rules/model';
import { useDestinations } from './rules/destinations';
import { Fallbacks, fallbacksFromPolicies } from './rules/fallback';
import { CoversCell, FallbackSentence, RuleSentence, RuleStatusLabel } from './rules/RuleParts';
import RuleEditorModal, { EditorRequest, RuleAccount } from './rules/RuleEditorModal';
import FallbackModal from './rules/FallbackModal';
import TestNotificationModal from './rules/TestNotificationModal';
import OnlySomeAccountsModal from './rules/OnlySomeAccountsModal';

const NOTIFICATIONS_TABLE_ID = 'Notifications';
const HEADERS = [
  // Status and actions are sized to their content, not counting the cells' own padding
  // (the widest label is "Paused · 1h left"); the rule's sentence takes the rest.
  { name: 'Rule', width: 'auto' },
  { name: 'Covers', width: '12%' },
  { name: 'Created by', width: '12%' },
  { name: 'Status', width: '92px' },
  { name: '', width: '64px' },
];

type Filter = 'all' | RuleTypeKey;

const Notifications = () => {
  const canWrite = canManage('notifications', 'Write');
  const [rules, setRules] = useState<Rule[]>([]);
  const [accounts, setAccounts] = useState<RuleAccount[]>([]);
  const [fallbacks, setFallbacks] = useState<Fallbacks>(fallbacksFromPolicies([]));
  const [loading, setLoading] = useState(true);
  const [loadFailed, setLoadFailed] = useState(false);
  const [filter, setFilter] = useState<Filter>('all');
  const [query, setQuery] = useState('');
  const [page, setPage] = useState(0);
  const [rowsPerPage, setRowsPerPage] = useState<number>(apiUser.getUserPreferencesTablePageSize() || 10);
  const [editor, setEditor] = useState<EditorRequest | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Rule | null>(null);
  const [testOpen, setTestOpen] = useState(false);
  const [fallbackFocus, setFallbackFocus] = useState<RuleTypeKey | null | undefined>(undefined);
  const [onlyOpen, setOnlyOpen] = useState(false);
  const [flashId, setFlashId] = useState<string | null>(null);
  const destinations = useDestinations(canWrite);

  const load = useCallback(async () => {
    const [rulesRes, accountsRes, policiesRes] = await Promise.all([
      apiNotifications.listRules(),
      apiDashboard.getCloudAccounts().catch(() => []),
      apiNotifications.getDefaultPolicies(),
    ]);
    setLoadFailed(!!rulesRes.error);
    setRules((rulesRes.rows || []).map(ruleFromRow));
    setAccounts((accountsRes || []).map((a: any) => ({ id: a.id, name: a.account_name, provider: a.cloud_provider || 'Other' })));
    setFallbacks(fallbacksFromPolicies(policiesRes.policies || []));
    setLoading(false);
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const accountName = useMemo(() => {
    const names = new Map(accounts.map((a) => [a.id, a.name]));
    return (id: string) => names.get(id) || 'Unknown account';
  }, [accounts]);
  const accountIds = useMemo(() => accounts.map((a) => a.id), [accounts]);

  const openNew = (patch: Partial<Rule>, pauseFor?: EditorRequest['pauseFor']) => {
    const types = patch.types || [filter !== 'all' ? filter : 'troubleshoot'];
    // There is one daily email rule; adding one edits it.
    const daily = types !== 'all' && types[0] === 'daily_recap' ? rules.find((r) => r.types !== 'all' && r.types.includes('daily_recap')) : undefined;
    if (daily) {
      setEditor({ rule: daily, isNew: false });
      return;
    }
    const ek = types !== 'all' && types.length === 1 ? RULE_TYPE[types[0]].defaultEvents : undefined;
    setEditor({ rule: newRule({ events: ek || null, ...patch, types }), isNew: true, pauseFor });
  };

  // Admin Overview "Create a … alert" / "Schedule a weekly digest" quick-links.
  useAdminAction(['alert-troubleshoot', 'alert-optimize', 'alert-slo', 'alert-weekly_digest'], (action: string) => {
    if (!canWrite) return;
    const key = action.replace('alert-', '');
    if (isRuleTypeKey(key)) openNew({ types: [key] });
  });

  const saveRule = async (rule: Rule, message: string, extra: Record<string, any> = {}) => {
    const res = await apiNotifications.upsertRule({ ...toUpsertInput(rule), ...extra });
    if (res.error) toast.error(res.error);
    else toast.success(message);
    load();
  };

  const runFix = async (issue: RuleIssue) => {
    if (!issue.fix) return;
    if (issue.fix.kind === 'delete') {
      const res: any = await apiNotifications.deleteNotificationRule(issue.fix.rule.id);
      if (res?.data?.data?.notification_rule_delete) toast.success('Rule removed');
      else toast.error('Couldn’t remove the rule. Try again in a minute.');
      load();
      return;
    }
    await saveRule(issue.fix.rule, `Updated “${issue.fix.rule.name}”`);
  };

  const showRule = (id: string) => {
    setTestOpen(false);
    setFilter('all');
    setQuery('');
    const index = rules.findIndex((r) => r.id === id);
    if (index >= 0) setPage(Math.floor(index / rowsPerPage));
    setFlashId(id);
    setTimeout(() => document.getElementById(`rule-${id}`)?.scrollIntoView({ behavior: 'smooth', block: 'center' }), 50);
    setTimeout(() => setFlashId(null), 1800);
  };

  const visible = rules.filter((r) => {
    if (filter !== 'all' && !typesOf(r).includes(filter)) return false;
    if (!query) return true;
    return `${r.name} ${whatText(r)} ${whoText(r, accountName)} ${r.createdBy}`.toLowerCase().includes(query.toLowerCase());
  });
  const pageRules = visible.slice(page * rowsPerPage, (page + 1) * rowsPerPage);
  const filterType = filter !== 'all' ? filter : null;

  const addMenu = [
    { id: 'add-rule-blank', label: 'Blank rule', onSelect: () => openNew({}) },
    {
      id: 'add-rule-stop-account',
      label: 'Stop everything from an account',
      onSelect: () => openNew({ types: 'all', scope: 'some', action: 'drop' }),
    },
    {
      id: 'add-rule-pause',
      label: 'Pause alerts for a while',
      onSelect: () => openNew({ types: ['troubleshoot', 'slo'], scope: 'some', action: 'drop' }, '24h'),
    },
    { id: 'add-rule-only-accounts', label: 'Only get alerts from some accounts', onSelect: () => setOnlyOpen(true) },
    {
      id: 'add-rule-mute-automation',
      label: 'Mute automation chatter',
      onSelect: () => openNew({ types: ['automation'], events: RULE_TYPE.automation.defaultEvents!, action: 'drop' }),
    },
  ];

  const ruleRow = (rule: Rule) => {
    const issues = ruleIssues(rule, rules, accountIds, accountName).filter((i) => i.kind !== 'info');
    const name = (
      <Typography id={`rule-${rule.id}`} sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>
        {rule.name}
      </Typography>
    );
    const row: any = [
      {
        component: (
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
            {rule.description ? (
              <Tooltip title={rule.description}>
                <span>{name}</span>
              </Tooltip>
            ) : (
              name
            )}
            <Box sx={{ fontSize: ds.text.body, color: ds.gray[600], lineHeight: 1.7 }}>
              <RuleSentence rule={rule} accountName={accountName} />
            </Box>
            {issues.map((issue) => (
              <Callout
                key={issue.text}
                tone={issue.kind === 'critical' ? 'critical' : issue.kind === 'warning' ? 'warning' : 'info'}
                message={issue.text}
                action={issue.fix && canWrite ? { label: issue.fix.label, id: `fix-${rule.id}`, onClick: () => runFix(issue) } : undefined}
              />
            ))}
          </Box>
        ),
        data: rule.name,
      },
      { component: <CoversCell rule={rule} /> },
      {
        component: (
          <Box id={`created-by-${rule.id}`} sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[0] }}>
            <Text value={rule.createdBy || '-'} />
            {rule.createdAt && <Datetime value={rule.createdAt} baseDate={new Date()} sxSecondary />}
          </Box>
        ),
        data: rule.createdBy,
      },
      { component: <RuleStatusLabel rule={rule} /> },
      {
        component: canWrite ? (
          <Box sx={{ display: 'flex', justifyContent: 'flex-end', alignItems: 'center', gap: ds.space[1] }}>
            <DsButton
              tone='ghost'
              composition='icon-only'
              aria-label={`Edit ${rule.name}`}
              id={`${rule.name}-edit`}
              onClick={() => setEditor({ rule, isNew: false })}
              icon={<SafeIcon src={EditIcon} alt='' width={18} height={18} />}
            />
            <DropdownMenu
              align='end'
              trigger={
                <DsButton
                  tone='ghost'
                  composition='icon-only'
                  aria-label={`More actions for ${rule.name}`}
                  id={`${rule.name}-more`}
                  icon={<MoreVert />}
                />
              }
              items={[
                ...(rule.active
                  ? []
                  : [
                      {
                        id: `${rule.name}-turn-on`,
                        label: 'Turn on',
                        onSelect: () => saveRule({ ...rule }, `“${rule.name}” is on`, { is_active: true }),
                      },
                    ]),
                {
                  id: `${rule.name}-duplicate`,
                  label: 'Duplicate',
                  onSelect: () => setEditor({ rule: { ...rule, id: undefined, name: `${rule.name} copy` }, isNew: true, named: true }),
                },
                {
                  id: `${rule.name}-delete`,
                  label: 'Delete',
                  tone: 'danger' as const,
                  icon: <SafeIcon src={DeleteIconRed} alt='' width={14} height={14} />,
                  onSelect: () => setDeleteTarget(rule),
                },
              ]}
            />
          </Box>
        ) : (
          <></>
        ),
      },
    ];
    if (flashId === rule.id) row.sx = { '& td': { backgroundColor: ds.blue[100] } };
    return row;
  };

  // What happens to notifications no rule covers, above the table so it is seen before the rules.
  const fallbackLine = (
    <Box sx={{ px: ds.space[5], pt: ds.space[3] }}>
      <Box
        id='fallback'
        sx={{
          display: 'flex',
          alignItems: 'center',
          flexWrap: 'wrap',
          gap: `${ds.space[1]} ${ds.space[2]}`,
          padding: `${ds.space[1]} ${ds.space[1]} ${ds.space[1]} ${ds.space[3]}`,
          borderRadius: ds.radius.md,
          backgroundColor: ds.background[200],
          fontSize: ds.text.body,
          color: ds.gray[600],
        }}
      >
        <Typography component='span' sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], whiteSpace: 'nowrap' }}>
          When no rule matches:{' '}
        </Typography>
        <Box component='span' sx={{ flex: 1, minWidth: 220, lineHeight: 1.7 }}>
          <FallbackSentence fallbacks={fallbacks} type={filterType} />
        </Box>
        {canWrite && filterType !== 'daily_recap' && filterType !== 'llm_budget' && (
          // Puts the pencil directly above the rows' edit buttons in the actions column.
          <Box sx={{ mr: '18px' }}>
            <DsButton
              id='edit-fallback'
              tone='ghost'
              size='sm'
              aria-label='Edit when no rule matches'
              icon={<SafeIcon src={EditIcon} alt='' width={14} height={14} />}
              onClick={() => setFallbackFocus(filterType)}
            >
              Edit
            </DsButton>
          </Box>
        )}
      </Box>
    </Box>
  );

  const emptyRow: any = [
    {
      component: (
        <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-start', gap: ds.space[1], py: ds.space[3] }}>
          <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>
            {query ? `No rules match “${query}”` : `No rules for ${filterType ? RULE_TYPE[filterType].label : 'anything'} yet`}
          </Typography>
          <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>
            Notifications without a rule follow “When no rule matches” above the table.
          </Typography>
        </Box>
      ),
    },
    { component: <></> },
    { component: <></> },
    { component: <></> },
    {
      component:
        canWrite && !query ? (
          <DsButton id='empty-add-rule' tone='secondary' size='sm' onClick={() => openNew({})}>
            Add rule
          </DsButton>
        ) : (
          <></>
        ),
    },
  ];

  const tableData = loading ? [] : pageRules.length ? pageRules.map(ruleRow) : [emptyRow];

  const count = (key: Filter) => (key === 'all' ? rules.length : rules.filter((r) => typesOf(r).includes(key)).length);
  const filterChip = (key: Filter) => (
    <Tooltip key={key} title={key === 'all' ? 'Every rule' : RULE_TYPE[key].includes.join(' ')}>
      <span>
        <Chip
          id={`filter-${key}`}
          variant='filter'
          size='sm'
          pressed={filter === key}
          count={count(key)}
          icon={key === 'all' ? undefined : <SafeIcon src={RULE_TYPE[key].icon} alt='' width={12} height={12} />}
          onClick={() => {
            setFilter(key);
            setPage(0);
          }}
        >
          {key === 'all' ? 'All' : RULE_TYPE[key].label}
        </Chip>
      </span>
    </Tooltip>
  );

  return (
    <Box position='relative'>
      <ListingLayout id='notification-container'>
        <ListingLayout.Toolbar
          actions={
            <>
              <Box sx={{ width: 200 }}>
                <Input
                  id='search-rules'
                  size='sm'
                  placeholder='Search rules'
                  aria-label='Search rules'
                  leadingIcon={<SearchIcon fontSize='small' />}
                  value={query}
                  onChange={(value: string) => {
                    setQuery(value);
                    setPage(0);
                  }}
                />
              </Box>
              <DsButton id='test-notification' tone='secondary' size='md' onClick={() => setTestOpen(true)}>
                Check where it goes
              </DsButton>
              {canWrite && (
                <DropdownMenu
                  align='end'
                  items={addMenu}
                  trigger={
                    <DsButton id='notification-rule' tone='primary' size='md' icon={<ExpandMore />} iconPlacement='end'>
                      Add rule
                    </DsButton>
                  }
                />
              )}
            </>
          }
        >
          <Box role='tablist' aria-label='Filter by type' sx={{ display: 'flex', flexWrap: 'wrap', gap: ds.space[1] }}>
            {filterChip('all')}
            {RULE_TYPES.map((t) => filterChip(t.key))}
          </Box>
        </ListingLayout.Toolbar>
        {filterType && RULE_TYPE[filterType].rollup && (
          <Typography sx={{ px: ds.space[4], fontSize: ds.text.small, color: ds.gray[600] }}>
            {`${RULE_TYPE[filterType].rollup!.charAt(0).toUpperCase()}${RULE_TYPE[filterType].rollup!.slice(1)}`} cover several accounts; each channel
            only gets the accounts its rules allow.
          </Typography>
        )}
        {filterType === 'automation' && (
          <Typography sx={{ px: ds.space[4], fontSize: ds.text.small, color: ds.gray[600] }}>
            Approval requests always go out; no rule can stop them.
          </Typography>
        )}
        {!loading && fallbackLine}
        <ListingLayout.Body>
          <CustomTable
            headers={HEADERS}
            tableData={tableData}
            rowsPerPage={rowsPerPage}
            totalRows={visible.length || 1}
            pageNumber={page + 1}
            onPageChange={(next: number, limit: number) => {
              setPage(next - 1);
              setRowsPerPage(limit);
            }}
            onSortChange={() => undefined}
            id={NOTIFICATIONS_TABLE_ID}
            loading={loading}
            emptyStateText={loadFailed ? "Couldn't load notification rules. Try again in a minute." : undefined}
            showExpandable={false}
            upperHeaders={[]}
          />
        </ListingLayout.Body>
      </ListingLayout>

      <RuleEditorModal
        request={editor}
        rules={rules}
        accounts={accounts}
        destinations={destinations}
        onClose={() => setEditor(null)}
        onSaved={() => {
          setEditor(null);
          load();
        }}
      />
      <FallbackModal
        open={fallbackFocus !== undefined}
        focusType={fallbackFocus ?? null}
        fallbacks={fallbacks}
        destinations={destinations}
        onClose={() => setFallbackFocus(undefined)}
        onSaved={load}
      />
      <TestNotificationModal open={testOpen} defaultType={filterType} accounts={accounts} onClose={() => setTestOpen(false)} onShowRule={showRule} />
      <OnlySomeAccountsModal
        open={onlyOpen}
        accounts={accounts}
        destinations={destinations}
        rules={rules}
        onClose={() => setOnlyOpen(false)}
        onSaved={load}
      />
      <DeleteNotificationRuleModal
        open={!!deleteTarget}
        handleClose={() => setDeleteTarget(null)}
        ruleData={deleteTarget || {}}
        sentence={deleteTarget ? <RuleSentence rule={deleteTarget} accountName={accountName} /> : null}
        listNotificationRules={load}
      />
    </Box>
  );
};

export default Notifications;
