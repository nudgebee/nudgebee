import React from 'react';
import { Box } from '@mui/material';
import ShieldOutlinedIcon from '@mui/icons-material/ShieldOutlined';
import RuleOutlinedIcon from '@mui/icons-material/RuleOutlined';
import { useRouter } from 'next/router';
import { ToggleGroup } from '@ui/ToggleGroup';
import SafeIcon from '@shared/icons/SafeIcon';
import { BetaIcon } from '@assets';
import { getUserSession } from '@lib/auth';
import { ds } from '@utils/colors';
import EgressFilterAdminTab from './EgressFilterAdminTab';
import ToolApprovalsAdminTab from './ToolApprovalsAdminTab';

const SecurityAdminTab = () => {
  const router = useRouter();
  const routed = router.pathname === '/user-management';
  const session = getUserSession();
  const canReadToolApprovals =
    session?.roles?.some((role) => role === 'tenant_admin' || role === 'tenant_admin_readonly') ||
    session?.isSuperAdmin ||
    session?.isSuperAdminReadonly;
  const [modalTab, setModalTab] = React.useState('egress');
  const tab = routed && canReadToolApprovals && router.asPath.split('#')[1]?.endsWith('/tool-approvals') ? 'tool-approvals' : modalTab;
  const options = [
    { value: 'egress', label: 'Egress Filter', icon: <ShieldOutlinedIcon sx={{ fontSize: 14 }} /> },
    ...(canReadToolApprovals
      ? [
          {
            value: 'tool-approvals',
            // New; Beta leaves room to change it.
            label: (
              <Box component='span' sx={{ display: 'inline-flex', alignItems: 'flex-start', gap: ds.space[1] }}>
                Tool Approvals
                <Box component='span' sx={{ display: 'inline-flex', marginTop: ds.space.mul(0, -5) }}>
                  <SafeIcon src={BetaIcon} alt='Beta icon' style={{ height: ds.space.mul(0, 10), width: ds.space.mul(0, 12) }} />
                </Box>
              </Box>
            ),
            icon: <RuleOutlinedIcon sx={{ fontSize: 14 }} />,
          },
        ]
      : []),
  ];

  const handleTabChange = (next) => {
    if (!routed) {
      setModalTab(next);
      return;
    }
    const [pathWithQuery] = router.asPath.split('#');
    const hash = next === 'tool-approvals' ? 'ai-tools/egress-filter/tool-approvals' : 'ai-tools/egress-filter';
    router.push(`${pathWithQuery}#${hash}`, undefined, { shallow: true, scroll: false }).catch(() => {});
  };

  return (
    <Box>
      <Box sx={{ mb: ds.space[3] }}>
        <ToggleGroup selection='single' value={tab} onChange={handleTabChange} options={options} size='md' ariaLabel='Security settings' />
      </Box>
      {tab === 'egress' ? <EgressFilterAdminTab /> : <ToolApprovalsAdminTab />}
    </Box>
  );
};

export default SecurityAdminTab;
