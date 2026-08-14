import { useEffect } from 'react';
import { Box, CircularProgress } from '@mui/material';
import { useData } from '@context/DataContext';
import { useRouter } from 'next/router';
import homeApi from '@api1/home';
import { transformClusters } from '@shared/layout/UpdateDataContext';

const isK8sProvider = (provider) => provider?.toUpperCase() === 'K8S';

const Kubernetes = () => {
  const router = useRouter();
  const { selectedCluster, setSelectedCluster } = useData();

  // `/kubernetes` is purely a redirector now, mirroring `/cloud-account`: with
  // no hash it opens the in-scope cluster's detail page, and every other case
  // lands on a page that owns the content it used to render inline.
  useEffect(() => {
    let cancelled = false;

    // window.location.hash, not router.asPath: on an auto-statically-optimized
    // page asPath can still be the server-rendered value on mount, and a dropped
    // hash here redirects an `#overview` deep link into a cluster instead.
    const hash = window.location.hash.replace('#', '');
    // Application Grouping moved to /dashboards; keep older links working.
    if (hash === 'groups') {
      router.replace('/dashboards#groups');
      return undefined;
    }
    // The fleet summary is no longer K8s-only — it lists every provider's
    // accounts and lives at /overview. Keep the old deep link working.
    if (hash) {
      router.replace('/overview#overview');
      return undefined;
    }

    const redirectToCluster = async () => {
      if (selectedCluster?.value && isK8sProvider(selectedCluster.cloud_provider)) {
        router.push(`/kubernetes/details/${selectedCluster.value}#summary`);
        return;
      }
      try {
        const accounts = await homeApi.getCloudAccounts();
        if (cancelled) {
          return;
        }
        const clusters = transformClusters(accounts.filter((account) => isK8sProvider(account.cloud_provider)));
        if (clusters.length > 0) {
          setSelectedCluster(clusters[0]);
          await router.push(`/kubernetes/details/${clusters[0].value}#summary`);
          return;
        }
      } catch (error) {
        console.error(error);
      }
      // No cluster to open (or the lookup failed) — the fleet-wide Account
      // Overview owns both the multi-account list and the connect-an-account
      // empty state, so send them there.
      if (!cancelled) {
        router.replace('/overview#overview');
      }
    };

    redirectToCluster();
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: '70vh' }}>
      <CircularProgress />
    </Box>
  );
};

export default Kubernetes;
