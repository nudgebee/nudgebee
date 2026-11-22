import PodTitleBox from '@components/k8s/pods/PodTitleBox';
import k8sApi from '@api1/kubernetes';
import { useRouter } from 'next/router';
import { useEffect, useState } from 'react';
import PodDetailsPage from '@components/k8s/pods/PodsDetails';
import { Box } from '@mui/material';
import { useData } from '@context/DataContext';
import { ds } from '@utils/colors';
import PermissionDeniedContent from '@components/common/navigation/PermissionDeniedContent';
import { hasPermission, isGrantsOnlyUser, missingPermissionMessage } from '@lib/auth';

const PodDetails = () => {
  const router = useRouter();

  const [podData, setPodData] = useState({});
  // Set on an access-denied GraphQL response -- getPodDetails otherwise
  // swallows that into an empty `cloud_resourses: []` with no indication why,
  // which rendered as a blank "Pod name: / ID: -" page instead of saying so.
  const [accessDenied, setAccessDenied] = useState(false);
  const { setPodLogRequest } = useData();

  useEffect(() => {
    if (!router.query.PodDetails) {
      return router.push('/kubernetes');
    }
    k8sApi.getPodDetails(router.query.PodDetails).then((res) => {
      if (res.accessDenied) {
        setAccessDenied(true);
        return;
      }
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

  // Matches KubernetesDetails' own TAB_REQUIRED_MODULE for the Apps & Infra /
  // Pods tab this page drills down from.
  if (accessDenied || (isGrantsOnlyUser() && !hasPermission('k8s', 'Read'))) {
    return <PermissionDeniedContent message={missingPermissionMessage('k8s:Read')} />;
  }

  const sx = {
    padding: `${ds.space[4]} ${ds.space[5]} ${ds.space[4]} ${ds.space[5]}`,
    borderRadius: ds.radius.xl,
    boxShadow: '0px 4px 4px 0px #00000026',
    alignSelf: 'stretch',
    backgroundColor: 'white',
  };
  return (
    <Box position={'relative'}>
      <PodTitleBox pod={podData} marginBottom={ds.space.mul(0, 3)} />
      <Box
        display='flex'
        flexDirection='column'
        alignItems='flex-start'
        sx={{ marginTop: ds.space[4], marginBottom: ds.space[3], scrollMarginTop: ds.space.mul(4, 5) }}
      >
        <Box sx={sx}>
          <PodDetailsPage pod={podData?.cloud_resourses} />
        </Box>
      </Box>
    </Box>
  );
};

export default PodDetails;
