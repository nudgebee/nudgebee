import PodTitleBox from '@components1/k8s/pods/PodTitleBox';
import k8sApi from '@api1/kubernetes';
import { useRouter } from 'next/router';
import { useEffect, useState } from 'react';
import PodDetailsPage from '@components1/k8s/pods/PodsDetails';
import { Box } from '@mui/material';
import { useData } from '@context/DataContext';

const PodDetails = () => {
  const router = useRouter();

  const [podData, setPodData] = useState({});
  const { setPodLogRequest } = useData();

  useEffect(() => {
    if (!router.query.PodDetails) {
      return router.push('/kubernetes');
    }
    k8sApi.getPodDetails(router.query.PodDetails).then((res) => {
      setPodData(res.data);
      if (res.data && res.data.cloud_resourses.length === 1) {
        const podObj = res.data.cloud_resourses[0];
        setPodLogRequest(podObj.account, {
          subject_name: podObj.name,
          subject_namespace: podObj?.meta?.namespace,
        });
      }
    });
  }, [router.query.PodDetails]);

  const sx = {
    padding: '20px 24px 20px 24px',
    borderRadius: '12px 12px 12px 12px',
    boxShadow: '0px 4px 4px 0px #00000026',
    alignSelf: 'stretch',
    backgroundColor: 'white',
  };
  return (
    <Box position={'relative'}>
      <PodTitleBox pod={podData} marginBottom={'6px'} />
      <Box display='flex' flexDirection='column' alignItems='flex-start' sx={{ marginTop: '16px', marginBottom: '12px', scrollMarginTop: '80px' }}>
        <Box sx={sx}>
          <PodDetailsPage pod={podData?.cloud_resourses} />
        </Box>
      </Box>
    </Box>
  );
};

export default PodDetails;
