import React, { useEffect, useMemo, useState } from 'react';
import dynamic from 'next/dynamic';
import RocketLaunchOutlinedIcon from '@mui/icons-material/RocketLaunchOutlined';
import InsightsOutlinedIcon from '@mui/icons-material/InsightsOutlined';
import ReceiptLongOutlinedIcon from '@mui/icons-material/ReceiptLongOutlined';
import ShieldOutlinedIcon from '@mui/icons-material/ShieldOutlined';
import SettingsOutlinedIcon from '@mui/icons-material/SettingsOutlined';
import AnchorComponent from '@components/common/navigation/AnchorComponent';
import ErrorBoundary from '@shared/ErrorBoundary';
import { Button as DsButton } from '@ui/Button';
import { EmptyState } from '@ui/EmptyState';
import { canOpenGatewayConfig, withAuth } from '@lib/auth';
import type { GroupId } from '@components/llm/gateway-usage/GatewayUsage';

const GatewayUsage = dynamic(() => import('@components/llm/gateway-usage/GatewayUsage'), { ssr: false });

/**
 * AI Gateway — its own sidenav section. Was the AI Gateway tab of /optimise; that
 * hash now redirects here (pages/optimise/index.jsx) so old links keep working.
 *
 * The top tab strip is the page's sections (GatewayUsage's `GROUPS`), which the
 * sidenav sub-items mirror. GatewayUsage reads the section off the hash itself and
 * stays mounted across sections, so a drill-in that crosses one keeps its scope.
 * The gateway's config stays in Admin → AI & Tools → Gateway; this page links to it.
 */

export async function getServerSideProps() {
  return {
    props: {
      enableLlmGateway: process.env.UI_ENABLE_LLM_GATEWAY === 'true',
      // Public base URL of the AI Gateway, surfaced to the Connect tab's setup
      // snippets. Empty when unset — the Connect tab shows a "not configured" state.
      llmGatewayUrl: process.env.LLM_GATEWAY_PUBLIC_URL || '',
    },
  };
}

const GATEWAY_CONFIG_PATH = '/user-management#ai-tools/gateway';

const SECTIONS: { id: GroupId; name: string; icon: React.ElementType }[] = [
  { id: 'connect', name: 'Connect', icon: RocketLaunchOutlinedIcon },
  { id: 'monitor', name: 'Monitor', icon: InsightsOutlinedIcon },
  { id: 'activity', name: 'Activity', icon: ReceiptLongOutlinedIcon },
  { id: 'governance', name: 'Governance', icon: ShieldOutlinedIcon },
];

interface AiGatewayProps {
  enableLlmGateway: boolean;
  llmGatewayUrl: string;
}

const AiGateway = ({ enableLlmGateway, llmGatewayUrl }: AiGatewayProps) => {
  // The gates read a client-populated session; resolving them after mount keeps
  // the first client render equal to the server HTML.
  const [isMounted, setIsMounted] = useState(false);
  useEffect(() => setIsMounted(true), []);

  const filterOptions = useMemo(
    () => SECTIONS.map((s, i) => ({ name: s.name, id: `ai-gateway-${s.id}`, fragment: s.id, value: i, icon: s.icon, iconSize: 18 })),
    []
  );

  if (!isMounted) {
    return null;
  }

  // Tenant-level: the usage actions admit every tenant role and scope rows to the
  // caller, so the deployment toggle is the only gate. Admin-only parts gate themselves.
  if (!enableLlmGateway) {
    return (
      <EmptyState
        size='page'
        illustration='no-permissions'
        title='AI Gateway is not available'
        description='The AI Gateway is not enabled on this deployment.'
      />
    );
  }

  return (
    <>
      <AnchorComponent
        manageRoute={true}
        disableHoverSubmenu
        filterOptions={filterOptions}
        buttonComponent={
          canOpenGatewayConfig() ? (
            <DsButton
              id='ai-gateway-configure-btn'
              href={GATEWAY_CONFIG_PATH}
              tone='secondary'
              size='md'
              composition='icon+text'
              icon={<SettingsOutlinedIcon fontSize='small' />}
            >
              Configure
            </DsButton>
          ) : undefined
        }
      />
      <ErrorBoundary>
        <GatewayUsage gatewayUrl={llmGatewayUrl} />
      </ErrorBoundary>
    </>
  );
};

export default withAuth(AiGateway);
