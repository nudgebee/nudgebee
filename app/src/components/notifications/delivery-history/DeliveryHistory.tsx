import React, { useEffect, useMemo, useState } from 'react';
import { useRouter } from 'next/router';
import dayjs from 'dayjs';
import { Box, Typography } from '@mui/material';
import { ListingLayout } from '@ui/ListingLayout';
import FilterDropdown from '@ui/FilterDropdown';
import { Banner } from '@ui/Banner';
import { Chip } from '@ui/Chip';
import { Label } from '@ui/Label';
import { Link } from '@ui/Link';
import CustomTable from '@shared/tables/CustomTable';
import apiUser from '@api1/user';
import deliveriesApi, { Delivery, DestinationSummary } from '@api1/notification/deliveries';
import { useBrandingConfig } from '@hooks/useTenantBranding';
import { useData } from '@context/DataContext';
import { ds } from '@utils/colors';
import DeliveryDetail from './DeliveryDetail';
import FailingDestinations from './FailingDestinations';
import {
  PLATFORM_FILTERS,
  STATUS_FILTERS,
  TYPE_FILTERS,
  destinationLabel,
  messageLink,
  platformName,
  routedBy,
  statusLine,
  summaryLabel,
  typeLabel,
} from './deliveryText';

const RANGES = [
  { label: 'Last 24 hours', value: '24h', hours: 24 },
  { label: 'Last 7 days', value: '7d', hours: 24 * 7 },
];
const HEADERS = ['Time', 'What was sent', 'Destination', 'Routed by', 'Status', 'Message'];
const RULES_HREF = '/user-management#notification-rules';

interface Option {
  label: string;
  value: string;
}

const sinceHours = (hours: number) => dayjs().subtract(hours, 'hour').toISOString();
const queryValue = (value: string | string[] | undefined) => (Array.isArray(value) ? value[0] : value) || '';
// A destination filter value is "<platform>:<destination id>"; it belongs to one platform filter.
const platformHolds = (platform: string, destination: string) =>
  !platform || !destination || Boolean(PLATFORM_FILTERS.find((p) => p.value === platform)?.platforms.includes(destination.split(':')[0]));

const Secondary = ({ children }: { children: React.ReactNode }) => (
  <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>{children}</Typography>
);

const RoutedByCell = ({ delivery }: { delivery: Delivery }) => {
  const routing = routedBy(delivery);
  if (routing.kind === 'default') return <Secondary>Default channel</Secondary>;
  if (routing.kind === 'requested') return <Secondary>Requested</Secondary>;
  if (routing.kind === 'none') return <Secondary>—</Secondary>;
  return (
    <Box sx={{ display: 'flex', flexWrap: 'wrap', columnGap: ds.space[2] }}>
      {delivery.status === 'not_sent' && <Secondary>Stopped by</Secondary>}
      {routing.rules.map((rule) => (
        <Box component='span' key={rule.id} data-testid={`delivery-history-rule-${delivery.id}-${rule.id}`}>
          <Link href={RULES_HREF}>{rule.name}</Link>
        </Box>
      ))}
    </Box>
  );
};

const StatusCell = ({ delivery, brand }: { delivery: Delivery; brand: string }) => {
  const status = statusLine(delivery, brand);
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-start', gap: ds.space[1] }}>
      <Label tone={status.tone} dot size='sm'>
        {status.label}
      </Label>
      {status.detail && <Secondary>{status.detail}</Secondary>}
    </Box>
  );
};

// Admin → Notification Rules → Delivery history: every notification sent or tried in the last 7 days,
// one row per send to a destination, reading as its last attempt.
const DeliveryHistory = () => {
  const router = useRouter();
  const { title: brand } = useBrandingConfig();
  const { allCluster } = useData();
  const alertFingerprint = queryValue(router.query.alert);
  const linkedType = TYPE_FILTERS.some((t) => t.value === queryValue(router.query.type)) ? queryValue(router.query.type) : '';
  const [range, setRange] = useState('7d');
  const [platform, setPlatform] = useState('');
  const [destination, setDestination] = useState('');
  const [status, setStatus] = useState('');
  const [type, setType] = useState(linkedType);
  const [fingerprint, setFingerprint] = useState(alertFingerprint);
  const [page, setPage] = useState(0);
  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [rows, setRows] = useState<Delivery[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [reload, setReload] = useState(0);
  const [failing, setFailing] = useState<DestinationSummary[]>([]);
  const [failingLoading, setFailingLoading] = useState(true);
  const [failingError, setFailingError] = useState(false);
  const [failingReload, setFailingReload] = useState(0);
  const [destinations, setDestinations] = useState<DestinationSummary[]>([]);

  const hours = RANGES.find((r) => r.value === range)?.hours ?? 24 * 7;

  // router.query is empty until hydration finishes, so a link's filters apply once it fills in.
  useEffect(() => {
    setFingerprint(alertFingerprint);
  }, [alertFingerprint]);

  useEffect(() => {
    setType(linkedType);
  }, [linkedType]);

  useEffect(() => {
    let cancelled = false;
    setFailingLoading(true);
    deliveriesApi.failingDestinations(sinceHours(24)).then((res) => {
      if (cancelled) return;
      setFailing(res.data);
      setFailingError(Boolean(res.error));
      setFailingLoading(false);
    });
    return () => {
      cancelled = true;
    };
  }, [reload, failingReload]);

  useEffect(() => {
    let cancelled = false;
    deliveriesApi.destinations(sinceHours(hours)).then((res) => {
      if (!cancelled) setDestinations(res.data);
    });
    return () => {
      cancelled = true;
    };
  }, [hours, reload]);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    const [destinationPlatform, ...destinationId] = destination ? destination.split(':') : [];
    deliveriesApi
      .list(
        {
          since: sinceHours(hours),
          platforms: PLATFORM_FILTERS.find((p) => p.value === platform)?.platforms,
          destination: destination ? { platform: destinationPlatform, destination: destinationId.join(':') } : null,
          status: status || undefined,
          kinds: TYPE_FILTERS.find((t) => t.value === type)?.kinds,
          fingerprint: fingerprint || undefined,
        },
        rowsPerPage,
        page * rowsPerPage
      )
      .then((res) => {
        if (cancelled) return;
        setRows(res.data.rows);
        setTotal(res.data.total);
        setError(res.error || '');
        setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [hours, platform, destination, status, type, fingerprint, page, rowsPerPage, reload]);

  const accountName = useMemo(() => {
    const names = new Map<string, string>((allCluster || []).map((c: any) => [c.value, c.label]));
    return (id?: string | null) => (id ? names.get(id) || '' : '');
  }, [allCluster]);

  const destinationOptions: Option[] = useMemo(
    () => destinations.map((d) => ({ label: `${summaryLabel(d)} · ${platformName(d.platform)}`, value: `${d.platform}:${d.destination}` })),
    [destinations]
  );

  const pick = (setter: (value: string) => void) => (_event: unknown, item?: Option | null) => {
    setter(item?.value || '');
    setPage(0);
  };

  const showDestination = (summary: DestinationSummary) => {
    setDestination(`${summary.platform}:${summary.destination}`);
    setPlatform('');
    setStatus('');
    setType('');
    setPage(0);
  };

  const tableData = useMemo(
    () =>
      rows.map((delivery) => {
        const link = messageLink(delivery);
        return [
          { text: dayjs(delivery.created_at).format('DD MMM, HH:mm') },
          {
            component: (
              <Box sx={{ minWidth: 0 }}>
                <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.medium }}>{delivery.title || typeLabel(delivery.kind)}</Typography>
                <Secondary>{`${typeLabel(delivery.kind)}${delivery.thread_id ? ' · in the alert’s thread' : ''}`}</Secondary>
              </Box>
            ),
          },
          {
            component: delivery.platform ? (
              <Box sx={{ minWidth: 0 }}>
                <Typography sx={{ fontSize: ds.text.body, overflowWrap: 'anywhere' }}>{destinationLabel(delivery)}</Typography>
                <Secondary>{platformName(delivery.platform)}</Secondary>
              </Box>
            ) : (
              <Secondary>—</Secondary>
            ),
          },
          { component: <RoutedByCell delivery={delivery} /> },
          { component: <StatusCell delivery={delivery} brand={brand} /> },
          {
            component: link ? (
              <Box component='span' data-testid={`delivery-history-message-${delivery.id}`} sx={{ whiteSpace: 'nowrap' }}>
                <Link href={link.href} openInNew>
                  {link.label}
                </Link>
              </Box>
            ) : (
              <Secondary>—</Secondary>
            ),
            // The expanded row reads the delivery off the last cell.
            _delivery: delivery,
            _accountName: accountName(delivery.account_id),
          },
        ];
      }),
    [rows, brand, accountName]
  );

  const filtered = range !== '7d' || platform || destination || status || type || fingerprint;
  const rangeLabel = RANGES.find((r) => r.value === range)?.label.toLowerCase() || 'the last 7 days';

  return (
    <ListingLayout id='delivery-history-container'>
      <Box sx={{ px: ds.space[5], pb: ds.space[4] }}>
        <FailingDestinations
          destinations={failing}
          loading={failingLoading}
          error={failingError}
          brand={brand}
          onShow={showDestination}
          onRetry={() => setFailingReload((n) => n + 1)}
        />
      </Box>
      <ListingLayout.Toolbar
        actions={
          <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>
            {`${total === 1 ? '1 delivery' : `${total} deliveries`} · newest first · kept 7 days`}
          </Typography>
        }
      >
        <FilterDropdown
          id='delivery-history-date-filter'
          label='Date'
          options={RANGES}
          value={RANGES.find((r) => r.value === range) ?? null}
          onSelect={pick((value) => setRange(value || '7d'))}
        />
        <FilterDropdown
          id='delivery-history-platform-filter'
          label='Platform'
          options={PLATFORM_FILTERS}
          value={PLATFORM_FILTERS.find((p) => p.value === platform) ?? null}
          onSelect={pick((value) => {
            setPlatform(value);
            if (!platformHolds(value, destination)) setDestination('');
          })}
        />
        <FilterDropdown
          id='delivery-history-destination-filter'
          label='Destination'
          options={destinationOptions}
          value={destinationOptions.find((d) => d.value === destination) ?? null}
          onSelect={pick((value) => {
            setDestination(value);
            if (!platformHolds(platform, value)) setPlatform('');
          })}
        />
        <FilterDropdown
          id='delivery-history-status-filter'
          label='Status'
          options={STATUS_FILTERS}
          value={STATUS_FILTERS.find((s) => s.value === status) ?? null}
          onSelect={pick(setStatus)}
        />
        <FilterDropdown
          id='delivery-history-type-filter'
          label='Type'
          options={TYPE_FILTERS}
          value={TYPE_FILTERS.find((t) => t.value === type) ?? null}
          onSelect={pick(setType)}
        />
        {fingerprint && (
          <Chip
            size='sm'
            onDismiss={() => {
              setFingerprint('');
              setPage(0);
            }}
            data-testid='delivery-history-alert-filter'
          >
            This alert
          </Chip>
        )}
      </ListingLayout.Toolbar>
      <ListingLayout.Body>
        {error && !loading ? (
          <Banner
            tone='critical'
            surface='section'
            title='Delivery history didn’t load'
            message='Check your connection, then try again.'
            actions={[{ label: 'Try again', onClick: () => setReload((n) => n + 1) }]}
          />
        ) : (
          <CustomTable
            id='delivery-history-table'
            loading={loading}
            headers={HEADERS}
            tableData={tableData}
            totalRows={total}
            rowsPerPage={rowsPerPage}
            pageNumber={page + 1}
            onPageChange={(nextPage: number, limit: number) => {
              setPage(nextPage - 1);
              setRowsPerPage(limit);
            }}
            showExpandable
            expandable={{ component: DeliveryDetail }}
            showUpdatedEmptyData
            emptyHeading={filtered ? 'No deliveries match these filters' : `No deliveries in ${rangeLabel}`}
            emptySubHeading={
              filtered
                ? 'Try a longer date range, or clear the filters.'
                : 'When a rule sends a notification, it shows up here. Deliveries are kept for 7 days.'
            }
          />
        )}
      </ListingLayout.Body>
    </ListingLayout>
  );
};

export default DeliveryHistory;
