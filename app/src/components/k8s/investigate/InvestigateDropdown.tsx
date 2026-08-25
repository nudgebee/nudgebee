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
  const [optionsData, setOptionsData] = useState<SelectOption[]>([]);
  const [accountId, setAccountId] = useState<string | string[] | undefined>('');

  useEffect(() => {
    if (accountId != router.query.accountId) {
      setAccountId(router?.query?.accountId);
    }
  }, [router.query.accountId]);

  useEffect(() => {
    if (optionsOverride) {
      setOptionsData(optionsOverride);
      return;
    }
    if (!query.id) {
      return;
    }
    const queryParams: any = {};

    if (subjectName) {
      queryParams.subject_name = subjectName;
    }
    if (subjectNamespace) {
      queryParams.subject_namespace = subjectNamespace;
    }
    if (accountId) {
      queryParams.account_id = accountId;
    }
    queryParams.finding_type = 'issue';
    if (accountId) {
      k8sApi
        .getK8sEventsName(10, 0, queryParams)
        .then((res: any) => {
          const options: SelectOption[] = (res?.data?.events ?? [])
            .filter((item: any) => !excludeIds?.includes(String(item.id)))
            .map((item: any) => ({
              value: String(item.id),
              label: item.title,
              subtext: dayjs(item.starts_at).fromNow(),
            }));
          setOptionsData(options);
        })
        .catch((e) => {
          console.error(e);
        });
    }
  }, [query, accountId, optionsOverride, excludeIds]);

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
