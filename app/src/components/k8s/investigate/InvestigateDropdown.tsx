import React, { useState, useEffect } from 'react';
import k8sApi from '@api1/kubernetes';
import { useRouter } from 'next/router';
import { Select, type SelectOption } from '@ui/Select';
import { Box, Typography } from '@mui/material';
import { ds } from 'src/utils/colors';
import SafeIcon from '@shared/icons/SafeIcon';
import { EventIconBlue } from '@assets';
import dayjs from 'dayjs';
import relativeTime from 'dayjs/plugin/relativeTime';

dayjs.extend(relativeTime);

interface InvestigateDropdownProps {
  query: any;
  inputMaxWidth: string;
  subjectName: string;
  subjectNamespace: string;
  resetStateWhenItemSelected: () => void;
  // When provided, the dropdown renders these options instead of fetching
  // same-subject events itself (used for the same-incident group, #34655).
  optionsOverride?: SelectOption[];
  // Events to omit from the fetched list — e.g. incident-group members, so
  // "Related Events" means "on this subject but outside this incident".
  excludeIds?: string[];
  title?: string;
  placeholder?: string;
}

const InvestigateDropdown: React.FC<InvestigateDropdownProps> = ({
  query,
  inputMaxWidth,
  subjectName,
  subjectNamespace,
  resetStateWhenItemSelected,
  optionsOverride,
  excludeIds,
  title = 'Related Events',
  placeholder = 'No related events found',
}) => {
  const router = useRouter();
  const [fetchedOptions, setFetchedOptions] = useState<SelectOption[]>([]);
  const accountId = router.query.accountId;

  useEffect(() => {
    if (optionsOverride || !query.id || !accountId) {
      return;
    }
    const queryParams: any = {};

    if (subjectName) {
      queryParams.subject_name = subjectName;
    }
    if (subjectNamespace) {
      queryParams.subject_namespace = subjectNamespace;
    }
    queryParams.account_id = accountId;
    queryParams.finding_type = 'issue';

    let cancelled = false;
    k8sApi
      .getK8sEventsName(10, 0, queryParams)
      .then((res: any) => {
        if (cancelled) {
          return;
        }
        const events = res?.data?.events;
        const options: SelectOption[] = (Array.isArray(events) ? events : [])
          .filter((item: any) => !excludeIds?.includes(String(item.id)))
          .map((item: any) => ({
            value: String(item.id),
            label: item.title,
            subtext: dayjs(item.starts_at).fromNow(),
          }));
        setFetchedOptions(options);
      })
      .catch((e) => {
        if (!cancelled) {
          console.error(e);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [query, accountId, optionsOverride, excludeIds]);

  const optionsData = optionsOverride ?? fetchedOptions;

  const handleChange = (next: string) => {
    if (next) {
      resetStateWhenItemSelected();
      router.push(`/investigate?id=${next}&accountId=${router.query.accountId}`);
    }
  };

  const selectedId = router.query.id ? String(router.query.id) : null;
  const selectedValue = optionsData.find((o) => o.value === selectedId) ? selectedId : null;

  return (
    <Box sx={{ mt: 'var(--ds-space-5)' }}>
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--ds-space-1)',
          mb: 'var(--ds-space-2)',
          '&::after': { content: '""', height: '0.5px', flex: 1, backgroundColor: ds.gray[200] },
        }}
      >
        <SafeIcon src={EventIconBlue} alt='related events' style={{ width: '16px', height: '16px' }} />
        <Typography
          sx={{
            color: ds.gray[700],
            fontSize: 'var(--ds-text-body-lg)',
            fontWeight: 'var(--ds-font-weight-medium)',
            lineHeight: 'normal',
            whiteSpace: 'nowrap',
          }}
        >
          {title}
        </Typography>
      </Box>
      <Select
        options={optionsData}
        onChange={handleChange}
        value={selectedValue}
        minWidth={inputMaxWidth ?? '100%'}
        size='sm'
        id='investigate-other-events'
        placeholder={placeholder}
      />
    </Box>
  );
};

export default InvestigateDropdown;
