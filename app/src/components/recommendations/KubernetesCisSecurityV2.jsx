import { Box, Stack, Typography } from '@mui/material';
import { useEffect, useRef, useState } from 'react';
import recommendationApi, { getCisTicketReferenceId, RECOMMENDATION_STATUS } from '@api1/recommendation';
import { ListingLayout } from '@ui/ListingLayout';
import FilterDropdown from '@ui/FilterDropdown';
import { Button as DsButton } from '@ui/Button';
import DownloadButton from '@shared/buttons/DownloadButton';
import TicketCreatePopupForm from '@components/tickets/TicketCreatePopupForm';
import TicketsIcon from '@assets/sidebar-icon/tickets-icon.svg';
import ThreeDotsMenu from '@ui/ThreeDotsMenu';
import Text from '@shared/format/Text';
import WidgetCard from '@ui/WidgetCard';
import Datetime from '@shared/format/Datetime';
import { hasWriteAccess } from '@lib/auth';
import PropTypes from 'prop-types';
import RecommendationJobDetails from '@components/k8s/common/RecommendationJobDetails';
import { Divider } from '@ui/Divider';
import { action } from 'src/utils/actionStyles';
import { SeverityIcon } from '@ui/SeverityIcon';
import { toSeverityLevel } from '@utils/common';
import CustomTable from '@shared/tables/CustomTable';
import { Link } from '@ui/Link';
import TicketLink from '@shared/links/TicketLink';
import RefreshIcon from '@mui/icons-material/Refresh';
import { toast as snackbar } from '@ui/Toast';
import CisRulePanel from './security/CisRulePanel';

const CIS_HEADER = [
  { name: 'Rule', width: '25%' },
  { name: 'Description', width: '37%' },
  { name: 'Severity', width: '8%' },
  { name: 'Failures', width: '10%' },
  { name: 'Updated At', width: '10%' },
  { name: 'Actions', width: '5%' },
];
// Cross-account mode (the /optimise Security tab): the grouping is per
// (cluster, rule), so lead with the cluster.
const CIS_HEADER_MULTI = [
  { name: 'Cluster', width: '12%' },
  { name: 'Rule', width: '23%' },
  { name: 'Description', width: '27%' },
  { name: 'Severity', width: '8%' },
  { name: 'Failures', width: '10%' },
  { name: 'Updated At', width: '10%' },
  { name: 'Actions', width: '5%' },
];
const KubernetesCisSecurity = (props) => {
  const kubernetesSecurityTable = 'kubernetesSecurityTable';

  // Rows carry their own account_id. The page-level id is only a fallback, and
  // in cross-account mode it is a list — with more than one account in scope
  // there is no single right answer, so resolve to undefined and let the action
  // fail rather than file a ticket against an arbitrary cluster.
  const accountIdForRow = (item) => {
    if (item?.account_id) return item.account_id;
    const scope = props?.kubernetes?.id;
    if (!Array.isArray(scope)) return scope;
    return scope.length === 1 ? scope[0] : undefined;
  };

  const [kubernetesSecurity, setKubernetesSecurity] = useState([]);
  const rawSecurityRef = useRef([]);
  const [kubernetesSecurityCount, setKubernetesSecurityCount] = useState(0);
  const [totalKubernetesSecurityCount, setTotalKubernetesSecurityCount] = useState(0);
  const [isTicketCreateFormOpen, setIsTicketCreateFormOpen] = useState(false);
  const [ticketData, setTicketData] = useState({});
  const [page, setPage] = useState(0);
  const [recommendationStatus, setRecommendationStatus] = useState('Open');
  const [loading, setLoading] = useState(false);
  // The rule whose detail panel is open. Rules used to expand into an accordion;
  // a rule is the unit you act on, so it opens the side panel instead.
  const [panelRule, setPanelRule] = useState(null);

  const closeTicketCreateForm = () => {
    setIsTicketCreateFormOpen(false);
  };

  const getTicketDescription = (data) => {
    //generate ticket description
    let description = '';
    description += '**TestId**: ' + data?.rule_id + '\n';
    description += '**TestName**: ' + data?.rule_name + '\n';
    description += '**TestDesc**: ' + data?.rule_description + '\n';
    description += '**Severity**: ' + data?.severity + '\n';
    description += '**Breaches**: ' + data?.count + '\n';
    return description;
  };

  const onMenuClick = (menuItem, data) => {
    if (menuItem.id === 'create-ticket') {
      setTicketData(data);
      setIsTicketCreateFormOpen(true);
    }
  };

  const buildRow = (item) => {
    const menuItems = [
      {
        icon: TicketsIcon,
        label: item.ticket?.ticket_id ? `Ticket: ${item.ticket.ticket_id}` : 'Create Ticket',
        id: 'create-ticket',
        disabled: !!item.ticket?.ticket_id,
      },
    ];
    let data = [];
    if (props?.accountsById) {
      data.push({
        component: <Text value={props.accountsById[item.account_id] || item.account_id} showAutoEllipsis />,
        data: item.account_id,
      });
    }
    data.push({
      component: (
        <Stack direction='column' spacing={0.5}>
          <Link href={'https://www.cisecurity.org/benchmark/kubernetes'} openInNew>
            {item.rule_id}
          </Link>
          <Text showAutoEllipsis lineClamp={2} value={item?.rule_name} />
          {item.ticket && <TicketLink ticketURL={item.ticket?.url} ticketID={item.ticket?.ticket_id} showAutoEllipsis />}
        </Stack>
      ),
      drilldownQuery: {
        data: item,
        rule: item,
      },
      data: item.rule_id,
    });
    data.push({
      component: <Text showAutoEllipsis lineClamp={2} value={item?.rule_description} />,
    });
    data.push({
      component: <SeverityIcon level={toSeverityLevel(item.severity)} aria-label={item.severity || '-'} />,
      data: item.severity || '-',
    });
    data.push({
      component: <Text value={item?.count} />,
    });
    data.push({ component: <Datetime value={item.updated_at} /> });
    data.push({
      component: (
        <Box display={'flex'} flexDirection={'row'} alignItems={'space-between'} justifyContent={'flex-end'}>
          <ThreeDotsMenu sx={{ ...action.primary }} menuItems={menuItems} data={item} onMenuClick={onMenuClick} />
        </Box>
      ),
    });
    return data;
  };

  const listCisSecurityRecommendations = () => {
    if (!props?.kubernetes?.id) {
      return;
    }
    setLoading(true);
    setKubernetesSecurity([]);
    recommendationApi
      .getK8sSecurityCISRecommendationGroups({
        accountId: props?.kubernetes?.id,
        status: recommendationStatus,
        fetchTicket: true,
      })
      .then((res) => {
        setLoading(false);
        const rows = res?.data?.recommendation ?? [];
        rawSecurityRef.current = rows;
        const k8sRecommendationData = rows.map(buildRow);
        setKubernetesSecurity(k8sRecommendationData);
        setKubernetesSecurityCount(k8sRecommendationData?.length ?? 0);
      })
      .catch(() => {
        setLoading(false);
      });
  };

  useEffect(() => {
    listCisSecurityRecommendations();
  }, [props?.kubernetes?.id, page, recommendationStatus]);

  useEffect(() => {
    if (!props?.kubernetes?.id) {
      return;
    }
    recommendationApi
      .getK8sSecurityCISRecommendationGroups({
        accountId: props?.kubernetes?.id,
      })
      .then((res) => {
        setTotalKubernetesSecurityCount(res?.data?.recommendation?.length ?? 0);
      })
      .catch(() => {
        console.error('Error fetching total count');
      });
  }, [props?.kubernetes?.id]);

  const handleTicketSuccess = ({ ticketId, url } = {}) => {
    const referenceId = getCisTicketReferenceId(accountIdForRow(ticketData), ticketData?.rule_id);
    const idx = rawSecurityRef.current.findIndex((item) => getCisTicketReferenceId(item.account_id, item.rule_id) === referenceId);
    if (idx === -1) return;
    rawSecurityRef.current[idx] = { ...rawSecurityRef.current[idx], ticket: { ticket_id: ticketId, url } };
    setKubernetesSecurity((prev) => {
      const next = [...prev];
      next[idx] = buildRow(rawSecurityRef.current[idx]);
      return next;
    });
  };

  const handleTicketFailure = (res) => {
    snackbar.error(`Failed! ${res}.`);
  };

  const triggerRecommendationJob = () => {
    recommendationApi.createRecommendationJob(props?.kubernetes?.id, 'trivy_cis_scan').then(() => {
      alert('Scan Triggered Successfully, Data will be updated in Sometime');
    });
  };
  return (
    <>
      <TicketCreatePopupForm
        open={isTicketCreateFormOpen}
        handleClose={closeTicketCreateForm}
        onClose={closeTicketCreateForm}
        onSuccess={handleTicketSuccess}
        onFailure={handleTicketFailure}
        ticketData={{
          subject: 'CIS Compliance Issue - ' + ticketData.rule_name,
          description: getTicketDescription(ticketData),
          accountId: accountIdForRow(ticketData),
        }}
        ticketUrl={{}}
        reference={{
          id: getCisTicketReferenceId(accountIdForRow(ticketData), ticketData.rule_id),
          type: 'kubernetes',
        }}
      />
      <CisRulePanel
        open={Boolean(panelRule)}
        onClose={() => setPanelRule(null)}
        rule={panelRule}
        accountName={props?.accountsById?.[panelRule?.account_id] || undefined}
        accountsById={props?.accountsById}
        scopeAccountId={props?.kubernetes?.id}
        onCreateTicket={(rule) => {
          setTicketData(rule);
          setIsTicketCreateFormOpen(true);
        }}
      />
      {!props?.disableInfographic && (
        <Box sx={{ display: 'flex', gap: 'var(--ds-space-3)' }} mt={2} mb={2}>
          <WidgetCard sx={{ mt: 0, minWidth: '160px' }}>
            <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-regular)', color: 'var(--ds-gray-600)' }}>
              Total Recommendations
            </Typography>
            <Typography
              sx={{ fontSize: 'var(--ds-text-display)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-700)', lineHeight: 1.2 }}
            >
              {totalKubernetesSecurityCount}
            </Typography>
          </WidgetCard>
        </Box>
      )}
      <ListingLayout id='best-practices'>
        <ListingLayout.Toolbar
          actions={
            <>
              <RecommendationJobDetails jobName={'kube_bench_scan'} />
              <Divider orientation='vertical' color={'var(--ds-gray-200)'} sx={{ mx: 'var(--ds-space-2)', my: 1 }} />
              <DownloadButton onClick={() => ({ tableId: kubernetesSecurityTable })} />
              {!Array.isArray(props?.kubernetes?.id) && hasWriteAccess(props?.kubernetes?.id) && (
                <DsButton
                  id='triggerRecommendation'
                  tone='secondary'
                  size='md'
                  composition='icon-only'
                  icon={<RefreshIcon fontSize='small' />}
                  aria-label='Refresh'
                  tooltip='Trigger Scan'
                  onClick={triggerRecommendationJob}
                />
              )}
            </>
          }
        >
          {props?.leadingFilters}
          {(props?.enableFilters?.includes('status') ?? true) && (
            <FilterDropdown
              id='cis-filter-status'
              label='Status'
              options={RECOMMENDATION_STATUS}
              value={recommendationStatus}
              onSelect={(e) => {
                setRecommendationStatus(e?.target?.value);
                setPage(0);
              }}
            />
          )}
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
          <CustomTable
            id={kubernetesSecurityTable}
            headers={props?.accountsById ? CIS_HEADER_MULTI : CIS_HEADER}
            tableData={kubernetesSecurity}
            rowsPerPage={kubernetesSecurityCount}
            totalRows={kubernetesSecurityCount}
            onPageChange={undefined}
            pageNumber={page + 1}
            stickyColumnIndex={props?.accountsById ? '7' : '6'}
            showUpdatedEmptyData={props.showUpdatedEmptyData}
            onRowClick={(query) => query?.rule && setPanelRule(query.rule)}
            loading={loading}
            tableHeadingCenter={['Actions', 'Severity']}
          />
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

KubernetesCisSecurity.propTypes = {
  accountsById: PropTypes.object,
  leadingFilters: PropTypes.node,
  heading: PropTypes.string,
  kubernetes: PropTypes.object,
  disableInfographic: PropTypes.bool,
  enableFilters: PropTypes.array,
  showUpdatedEmptyData: PropTypes.bool,
};

export default KubernetesCisSecurity;
