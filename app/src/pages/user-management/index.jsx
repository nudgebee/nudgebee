import React, { useEffect } from 'react';
import ErrorBoundary from '@components1/common/ErrorBoundary';
import AllUsers from '@components1/user-management/AllUsers';
import UserGroup from '@components1/user-management/UserGroup';
import AnchorComponent from '@components1/common/AnchorComponent';
import { AuditsTable } from '@components1/audits';
import { Box } from '@mui/material';
import Notifications from '@components1/notifications';
import Integrations from '@components1/accounts/integration';
import ApprovalPolicy from '@components1/autopilot/ApprovalPolicy';
import Billing from '@components1/billing';
import { AuditIcon, BillingIcon, NotificationIcon1, User1, UserGroupIcon, IntegrationsIcon } from '@assets';
import { useSession } from 'next-auth/react';
import { useRouter } from 'next/router';

const filterOptions = [
  {
    name: 'Users',
    value: 0,
    disabled: false,
    fragment: 'users',
    icon: User1,
  },
  {
    name: 'Groups',
    value: 1,
    disabled: false,
    fragment: 'groups',
    icon: UserGroupIcon,
  },
  {
    name: 'Audits',
    value: 2,
    disabled: false,
    fragment: 'audits',
    icon: AuditIcon,
  },
  {
    name: 'Notifications',
    value: 3,
    disabled: false,
    fragment: 'notifications',
    icon: NotificationIcon1,
  },
  {
    name: 'Integrations',
    value: 4,
    disabled: false,
    fragment: 'integrations',
    icon: IntegrationsIcon,
  },
  {
    name: 'Billing',
    value: 5,
    disabled: true,
    fragment: 'billing',
    icon: BillingIcon,
    betaIcon: true,
  },
  // {
  //   name: 'Auto Pilot Approval Policy',
  //   value: 6,
  //   disabled: false,
  //   fragment: 'runbook-approval-policy',
  //   icon: ApprovalPolicyIcon,
  //   betaIcon: true,
  // },
];
export default function UserManagement() {
  const router = useRouter();
  const [selectedFilter, setSelectedFilter] = React.useState(0);
  const sessionData = useSession({ required: true });
  let options = filterOptions;
  if (sessionData?.data?.onPrem) {
    options = filterOptions.filter((f) => f.value !== 5);
  }
  useEffect(() => {
    const fragment = router.asPath.split('#')[1];
    const option = filterOptions.find((option) => option.fragment == fragment);
    if (option) {
      setSelectedFilter(option.value);
    }
  }, []);

  return (
    <>
      <AnchorComponent
        manageRoute={true}
        options={options[selectedFilter]?.options || []}
        filterOptions={options}
        onChangeFilter={(val) => {
          setSelectedFilter(val);
        }}
      />
      <ErrorBoundary key={selectedFilter}>
        <Box mt={2}>
          {selectedFilter === 0 && <AllUsers />}
          {selectedFilter === 1 && <UserGroup />}
          {selectedFilter === 2 && <AuditsTable />}
          {selectedFilter === 3 && <Notifications />}
          {selectedFilter === 4 && <Integrations />}
          {selectedFilter === 5 && <Billing />}
          {selectedFilter === 6 && <ApprovalPolicy />}
        </Box>
      </ErrorBoundary>
    </>
  );
}
