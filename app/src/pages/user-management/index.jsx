import React, { useEffect } from 'react';
import ErrorBoundary from '@common-new/ErrorBoundary';
import AllUsers from '@components1/user-management/AllUsers';
import UserGroup from '@components1/user-management/UserGroup';
import AnchorComponent from '@common-new/AnchorComponent';
import { AuditsTable } from '@components1/audits';
import { Box } from '@mui/material';
import Notifications from '@components1/notifications';
import Integrations from '@components1/accounts/integration';
import { AuditIcon, NotificationIcon1, User1, UserGroupIcon, IntegrationsIcon } from '@assets';
import { useSession } from 'next-auth/react';
import { useRouter } from 'next/router';
import { userManagementFilters } from '@lib/authHooks';

// Base filters that ship in OSS. Extensions register additional filters via
// registerUserManagementFilter — those slot in at the end (e.g. billing on
// saas-tier deployments).
const baseFilters = [
  { name: 'Users', fragment: 'users', icon: User1, Body: AllUsers },
  { name: 'Groups', fragment: 'groups', icon: UserGroupIcon, Body: UserGroup },
  { name: 'Audits', fragment: 'audits', icon: AuditIcon, Body: AuditsTable },
  { name: 'Notifications', fragment: 'notifications', icon: NotificationIcon1, Body: Notifications },
  { name: 'Integrations', fragment: 'integrations', icon: IntegrationsIcon, Body: Integrations },
];

export default function UserManagement() {
  const router = useRouter();
  const sessionData = useSession({ required: true });
  const session = sessionData?.data;

  // Combine base filters with any registered extensions filtered by session,
  // then stamp positional values so AnchorComponent's routing keeps working.
  const filterOptions = React.useMemo(() => {
    const all = [...baseFilters, ...userManagementFilters(session)];
    return all.map((f, i) => ({
      ...f,
      value: i,
      disabled: f.disabled ?? false,
    }));
  }, [session]);

  const [selectedFilter, setSelectedFilter] = React.useState(0);

  useEffect(() => {
    const fragment = router.asPath.split('#')[1];
    const option = filterOptions.find((opt) => opt.fragment == fragment);
    if (option) {
      setSelectedFilter(option.value);
    }
  }, [filterOptions, router.asPath]);

  const SelectedBody = filterOptions[selectedFilter]?.Body;

  return (
    <>
      <AnchorComponent
        manageRoute={true}
        options={filterOptions[selectedFilter]?.options || []}
        filterOptions={filterOptions}
        onChangeFilter={(val) => {
          setSelectedFilter(val);
        }}
      />
      <ErrorBoundary key={selectedFilter}>
        <Box mt={2}>{SelectedBody && <SelectedBody session={session} />}</Box>
      </ErrorBoundary>
    </>
  );
}
