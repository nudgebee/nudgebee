import React, { useState, useEffect } from 'react';
import k8sApi from '@api1/kubernetes';
import { useRouter } from 'next/router';
import { Select, type SelectOption } from '@components1/ds/Select';
import { Box, Typography } from '@mui/material';
import { ds } from 'src/utils/colors';
import SafeIcon from '@common/SafeIcon';
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
}

const InvestigateDropdown: React.FC<InvestigateDropdownProps> = ({
  query,
  inputMaxWidth,
  subjectName,
  subjectNamespace,
  resetStateWhenItemSelected,
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
          const options: SelectOption[] = (res?.data?.events ?? []).map((item: any) => ({
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
  }, [query, accountId]);

  const handleChange = (next: string) => {
    if (next) {
      resetStateWhenItemSelected();
      router.push(`/investigate?id=${next}&accountId=${router.query.accountId}`);
    }
  };

  const selectedId = router.query.id ? String(router.query.id) : null;
  const selectedValue = optionsData.find((o) => o.value === selectedId) ? selectedId : null;

  return (
    <Box sx={{ mt: '24px' }}>
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: '6px',
          mb: '10px',
          '&::after': { content: '""', height: '0.5px', flex: 1, backgroundColor: ds.gray[200] },
        }}
      >
        <SafeIcon src={EventIconBlue} alt='related events' style={{ width: '16px', height: '16px' }} />
        <Typography sx={{ color: ds.gray[700], fontSize: '14px', fontWeight: 500, lineHeight: 'normal', whiteSpace: 'nowrap' }}>
          Related Events
        </Typography>
      </Box>
      <Select
        options={optionsData}
        onChange={handleChange}
        value={selectedValue}
        minWidth={inputMaxWidth ?? '100%'}
        size='sm'
        id='investigate-other-events'
        placeholder='No related events found'
      />
    </Box>
  );
};

export default InvestigateDropdown;
