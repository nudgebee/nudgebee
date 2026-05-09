import CustomDropdown from '@components1/common/CustomDropdown';
import React, { useState, useEffect } from 'react';
import k8sApi from '@api1/kubernetes';
import { useRouter } from 'next/router';
import { truncateText } from 'src/utils/common';
import { Box, Tooltip, Typography } from '@mui/material';
import Datetime from '@components1/common/format/Datetime';

interface InvestigateDropdownProps {
  query: any;
  inputMaxWidth: string;
  subjectName: string;
  subjectNamespace: string;
  resetStateWhenItemSelected: () => void;
}

const InvestigateDropdown: React.FC<InvestigateDropdownProps> = ({ query, subjectName, subjectNamespace, resetStateWhenItemSelected }) => {
  const router = useRouter();
  const [optionsData, setOptionsData] = useState([]);
  const [accountId, setAccountId] = useState<string | string[] | undefined>('');
  const [title, setTitle] = useState<string | null>(null);

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
          const optionsArray: any = [];
          let currentEventTitle = null;

          res?.data?.events?.map((item: any) => {
            const isSelected = router.query.id == item.id;

            optionsArray.push({
              label: (
                <Box
                  sx={{
                    display: 'flex',
                    justifyContent: 'space-between',
                    width: '100%',
                    backgroundColor: isSelected ? '#F3F4F6' : 'transparent',
                    fontWeight: isSelected ? 500 : 400,
                    padding: '4px 8px',
                    margin: '-4px -8px',
                    borderRadius: '4px',
                  }}
                >
                  <Tooltip title={item.title} placement='right' slotProps={{ popper: { modifiers: [{ name: 'flip', enabled: false }] } }}>
                    <Typography mr={'10px'} sx={{ fontWeight: 'inherit' }}>
                      {truncateText(item.title, 35)}
                    </Typography>
                  </Tooltip>
                  <Datetime
                    value={item.starts_at}
                    sx={{
                      color: '#374151',
                      fontSize: '10px',
                      fontWeight: 400,
                      marginBottom: '0px',
                    }}
                    sxSuffix={{
                      color: '#374151',
                      fontSize: '10px',
                      fontWeight: 400,
                      marginBottom: '0px',
                    }}
                    baseDate={new Date()}
                  />{' '}
                </Box>
              ),
              value: item.id,
              label1: item.title,
            });

            if (isSelected) {
              currentEventTitle = item.title;
            }
          });

          setOptionsData(optionsArray);
          if (currentEventTitle) {
            setTitle(currentEventTitle);
          }
        })
        .catch((e) => {
          console.error(e);
        });
    }
  }, [query, accountId]);

  const handleDropdownAction = (e: any, v: any) => {
    setTitle(v?.label1 || '');
    if (e.target.value) {
      resetStateWhenItemSelected();
      router.push(`/investigate?id=${e.target.value}&accountId=${router.query.accountId}`);
    }
  };

  return (
    <CustomDropdown
      label={'Related Events'}
      align={'right'}
      options={optionsData}
      onChange={handleDropdownAction}
      value={title}
      clusterData={{}}
      minWidth='240px'
      id='investigate-other-events'
      openDirection='up'
      noOptionsText='No related events found'
    />
  );
};

export default InvestigateDropdown;
