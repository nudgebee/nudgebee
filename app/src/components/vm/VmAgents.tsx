import React, { useCallback, useEffect, useState } from 'react';
import { Box, Typography } from '@mui/material';
import { useRouter } from 'next/router';
import CustomTable from '@shared/tables/CustomTable';
import ListingLayout from '@ui/ListingLayout';
import { Label } from '@ui/Label';
import { Button } from '@ui/Button';
import Datetime from '@shared/format/Datetime';
import apiVm, { VmAgent } from '@api1/vm';
import { CellText, useLatestRequest } from './common';
import { ds } from '@utils/colors';

const AGENT_HEADERS = ['Agent', 'Connection', 'Configuration', 'Version', 'Last Connected'];

/**
 * The foragers (vm_agent integrations) this account reaches its machines through.
 *
 * They are integrations, managed under Admin → Integrations; this is a read-only
 * fleet-side view so an operator debugging "why did the scan fail" does not have
 * to leave the page to see whether an agent is even connected.
 *
 * Rendered under Agent Health → Proxy Agent for a self-hosted account (it used to
 * be /vm#agents), above that tab's datasource health. No horizontal padding of
 * its own: it is a direct child of the page column, so the card lines up with the
 * tab strip above it.
 */
const VmAgents = ({ accountId }: { accountId: string }) => {
  const router = useRouter();
  const [loading, setLoading] = useState(false);
  const [agents, setAgents] = useState<VmAgent[]>([]);
  const beginRequest = useLatestRequest();

  const fetchAll = useCallback(() => {
    if (!accountId) return;
    setLoading(true);
    const isLatest = beginRequest();
    apiVm
      .listAgents(accountId)
      .then((agentRows) => {
        if (!isLatest()) return;
        setAgents(agentRows);
      })
      .catch((error) => {
        if (!isLatest()) return;
        console.error('Failed to load VM agents:', error);
        setAgents([]);
      })
      .finally(() => {
        if (isLatest()) setLoading(false);
      });
  }, [accountId, beginRequest]);

  useEffect(() => {
    fetchAll();
  }, [fetchAll]);

  const agentRows = agents.map((agent) => [
    { component: <CellText text={agent.name} /> },
    { component: <Label tone={agent.connected ? 'success' : 'critical'} size='sm' text={agent.connected ? 'Connected' : 'Disconnected'} /> },
    { component: <Label tone={agent.status === 'enabled' ? 'success' : 'neutral'} size='sm' text={agent.status || 'unknown'} /> },
    { component: <CellText text={agent.version} /> },
    { component: agent.last_seen_at ? <Datetime value={agent.last_seen_at} /> : <CellText text='—' /> },
  ]);

  // One relay connection per account, decorated onto every agent row by
  // listAgents — connected if any row reports it.
  const relayConnected = agents.some((agent) => agent.connected);

  // The Servers category of the integrations page — where vm_agent configs are
  // actually created and edited.
  const manageIntegrations = () => router.push('/user-management?integration=server#integrations');

  return (
    <Box sx={{ mt: 4 }}>
      <ListingLayout id='vm-agents'>
        <ListingLayout.Toolbar
          title='Proxy Agents'
          actions={
            <Button id='vm-agents-manage' tone='secondary' size='sm' onClick={manageIntegrations}>
              Manage
            </Button>
          }
        />
        <ListingLayout.Body>
          <CustomTable
            id='VM_AGENTS_TABLE'
            headers={AGENT_HEADERS}
            tableData={agentRows}
            loading={loading}
            emptyHeading='No proxy agent installed'
            emptySubHeading='Add a VM Agent integration to reach the machines in this account.'
            showUpdatedEmptyData={true}
          />
          {/* Same Features block the K8s agent card carries. A forager reaches the
              platform only through the relay, so its connection is the relay's —
              there is no separate relayConnection flag to read as there is on the
              in-cluster agent. Hidden while nothing is installed: the table's
              empty state already says what to do. */}
          {agents.length > 0 && (
            <>
              <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mt: 2, mb: 1 }}>Features</Typography>
              <ul>
                <li>
                  <b>Relay - </b>
                  {relayConnected ? 'Connected' : 'Disconnected'}
                </li>
              </ul>
            </>
          )}
        </ListingLayout.Body>
      </ListingLayout>
    </Box>
  );
};

export default VmAgents;
