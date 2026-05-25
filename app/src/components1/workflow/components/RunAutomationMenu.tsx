import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { useRouter } from 'next/router';
import { Box, CircularProgress, Typography } from '@mui/material';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import SettingsIcon from '@mui/icons-material/Settings';
import { Button } from '@components1/ds/Button';
import { DropdownMenu, type DropdownMenuItem } from '@components1/ds/DropdownMenu';
import apiWorkflow from '@api1/workflow';
import { snackbar } from '@components1/common/snackbarService';
import { parseHttpResponseBodyMessage } from 'src/utils/common';
import { colors } from 'src/utils/colors';
import { manualTriggerIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import Datetime from '@components1/common/format/Datetime';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import TriggerWorkflowModal from './TriggerWorkflowModal';
import { getDefaultTriggerInputs, getPrimaryTriggerType, getWorkflowInputSchema, hasManualTrigger } from '../utils/workflowTriggerHelpers';

interface RunAutomationMenuProps {
  accountId: string;
  disabled?: boolean;
}

interface WorkflowListItem {
  id: string;
  name: string;
  status?: string;
  definition?: any;
  tags?: any;
  created_at?: string;
  created_by_user?: { id?: string; display_name?: string } | null;
  last_execution_status?: string;
  last_execution_time?: string;
}

type LoadState = 'idle' | 'loading' | 'loaded' | 'error';

const WorkflowRow: React.FC<{ workflow: WorkflowListItem }> = ({ workflow }) => {
  const firstName = workflow.created_by_user?.display_name?.split(' ')[0];
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 1.5, minWidth: 0, py: 0.25, width: '100%' }}>
      {/* Left: name + created */}
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.5, minWidth: 0, flex: 1 }}>
        <Typography
          sx={{
            fontSize: '13px',
            fontWeight: 500,
            color: colors.text.primary,
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
          }}
        >
          {workflow.name}
        </Typography>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.75, whiteSpace: 'nowrap', overflow: 'hidden' }}>
          <Typography sx={{ fontSize: '11px', color: colors.text.secondaryDark, lineHeight: 1.4 }}>Created</Typography>
          {workflow.created_at && (
            <Datetime
              baseDate={new Date()}
              value={workflow.created_at}
              sxSuffix={{ fontSize: '11px', color: colors.text.tertiary }}
              sx={{ fontSize: '11px', color: colors.text.secondary }}
            />
          )}
          {firstName && <Typography sx={{ fontSize: '11px', color: colors.text.tertiary }}>· {firstName}</Typography>}
        </Box>
      </Box>

      {/* Right: last run (status chip stacked over relative time) */}
      <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: 0.25, flexShrink: 0, whiteSpace: 'nowrap' }}>
        {workflow.last_execution_status ? (
          <>
            <Typography sx={{ fontSize: '11px', color: colors.text.tertiarymedium, fontStyle: 'italic' }}>Last Run</Typography>

            <Box sx={{ display: 'flex' }}>
              <CustomLabels text={workflow.last_execution_status.toLowerCase()} textTransform='capitalize' />
              {workflow.last_execution_time && (
                <Datetime
                  baseDate={new Date()}
                  value={workflow.last_execution_time}
                  sxSuffix={{ fontSize: '11px', color: colors.text.tertiary }}
                  sx={{ fontSize: '11px', color: colors.text.secondary }}
                />
              )}
            </Box>
          </>
        ) : (
          <Typography sx={{ fontSize: '11px', color: colors.text.tertiarymedium, fontStyle: 'italic' }}>No runs yet</Typography>
        )}
      </Box>
    </Box>
  );
};

const RunAutomationMenu: React.FC<RunAutomationMenuProps> = ({ accountId, disabled = false }) => {
  const router = useRouter();
  const [workflows, setWorkflows] = useState<WorkflowListItem[]>([]);
  const [loadState, setLoadState] = useState<LoadState>('idle');
  const [errorMessage, setErrorMessage] = useState('');

  const [selectedWorkflow, setSelectedWorkflow] = useState<WorkflowListItem | null>(null);
  const [modalOpen, setModalOpen] = useState(false);
  const [triggerLoading, setTriggerLoading] = useState(false);

  const fetchWorkflows = useCallback(async () => {
    if (!accountId) return;
    setLoadState('loading');
    setErrorMessage('');
    try {
      const response: any = await apiWorkflow.listWorkflows(accountId, 'ACTIVE', undefined, 'manual', 100);
      const apiError = parseHttpResponseBodyMessage(response);
      if (apiError) {
        setErrorMessage(apiError);
        setLoadState('error');
        return;
      }
      const list: WorkflowListItem[] = response?.data?.workflow_list?.workflows || [];
      // Defense-in-depth: ensure each entry actually has a manual trigger,
      // in case the backend filter ever loosens or a workflow declares both
      // manual + event triggers and we only want the ones runnable by hand.
      const manualOnly = list.filter((w) => hasManualTrigger(w));
      setWorkflows(manualOnly);
      setLoadState('loaded');
    } catch (err) {
      console.error('Failed to load automations:', err);
      setErrorMessage('Failed to load automations');
      setLoadState('error');
    }
  }, [accountId]);

  // Pre-fetch on mount so the dropdown opens without a loading flicker on
  // first click. Cheap query and the button is only mounted when the user is
  // already in the investigation flow.
  useEffect(() => {
    if (accountId) fetchWorkflows();
  }, [accountId, fetchWorkflows]);

  const handleSelect = (workflow: WorkflowListItem) => {
    setSelectedWorkflow(workflow);
    setModalOpen(true);
  };

  const handleModalClose = () => {
    setModalOpen(false);
    setSelectedWorkflow(null);
    setTriggerLoading(false);
  };

  const handleTrigger = async (inputs: any) => {
    // Throw on every failure path. TriggerWorkflowModal calls `onClose()` only
    // after `await onTrigger(...)` resolves — returning a resolved promise on
    // error would close the modal and discard the user's input.
    if (!selectedWorkflow?.id || !accountId) {
      const msg = 'Invalid automation or account ID';
      snackbar.error(msg);
      throw new Error(msg);
    }
    setTriggerLoading(true);
    try {
      const response: any = await apiWorkflow.triggerWorkflow({
        account_id: accountId,
        id: selectedWorkflow.id,
        inputs,
      });
      const errorMsg = parseHttpResponseBodyMessage(response);
      if (errorMsg) throw new Error(errorMsg);
      const triggerData = response?.data?.workflow_trigger;
      if (!triggerData?.execution_id) throw new Error('Failed to trigger automation');
      snackbar.success(`Automation "${selectedWorkflow.name}" triggered`);
    } catch (err) {
      console.error('Error triggering automation:', err);
      const msg = err instanceof Error && err.message ? err.message : `Failed to trigger automation "${selectedWorkflow.name}"`;
      snackbar.error(msg);
      throw err;
    } finally {
      setTriggerLoading(false);
    }
  };

  const goToWorkflowsPage = () => {
    router.push(`/auto-pilot?accountId=${accountId}#workflow`);
  };

  const items: DropdownMenuItem[] = useMemo(() => {
    if (loadState === 'loading') {
      return [
        {
          label: (
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
              <CircularProgress size={14} />
              <Typography sx={{ fontSize: '13px', color: colors.text.secondaryDark }}>Loading automations…</Typography>
            </Box>
          ),
          disabled: true,
          onSelect: () => {},
        },
      ];
    }
    if (loadState === 'error') {
      return [
        {
          label: <Typography sx={{ fontSize: '13px', color: colors.error }}>{errorMessage || 'Failed to load automations'}</Typography>,
          disabled: true,
          onSelect: () => {},
        },
      ];
    }
    if (loadState === 'loaded' && workflows.length === 0) {
      return [
        {
          label: <Typography sx={{ fontSize: '13px', color: colors.text.secondaryDark }}>No automations configured</Typography>,
          disabled: true,
          onSelect: () => {},
        },
        { type: 'separator' as const },
        {
          label: 'Configure automations →',
          icon: <SettingsIcon fontSize='small' />,
          onSelect: goToWorkflowsPage,
          id: 'run-automation-configure',
        },
      ];
    }
    return workflows.map((w) => ({
      label: <WorkflowRow workflow={w} />,
      icon: <SafeIcon src={manualTriggerIcon} alt='manual trigger' height={14} width={14} />,
      onSelect: () => handleSelect(w),
      id: `run-automation-item-${w.id}`,
      searchText: w.name,
    }));
    // handleSelect / goToWorkflowsPage are stable for the lifetime of the
    // dropdown's open state — recomputing items on each change of selected
    // workflow would force the menu to remount and close.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loadState, workflows, errorMessage]);

  return (
    <>
      <Box sx={{ mr: '8px' }}>
        <DropdownMenu
          align='end'
          side='bottom'
          size='sm'
          minWidth={380}
          itemsMaxHeight={420}
          searchable
          searchPlaceholder='Search automations…'
          onRefresh={fetchWorkflows}
          refreshLabel='Refresh list'
          trigger={
            <Button
              id='run-automation-btn'
              data-testid='run-automation-btn'
              tone='secondary'
              size='sm'
              composition='text+icon'
              iconPlacement='end'
              icon={<KeyboardArrowDownIcon sx={{ fontSize: 16 }} />}
              tooltip='Run an automation'
              disabled={disabled || !accountId}
            >
              Automations
            </Button>
          }
          items={items}
        />
      </Box>

      {selectedWorkflow && (
        <TriggerWorkflowModal
          open={modalOpen}
          onClose={handleModalClose}
          workflowName={selectedWorkflow.name}
          triggerType={getPrimaryTriggerType(selectedWorkflow)}
          defaultInputs={getDefaultTriggerInputs(selectedWorkflow)}
          inputSchema={getWorkflowInputSchema(selectedWorkflow)}
          onTrigger={handleTrigger}
          loading={triggerLoading}
        />
      )}
    </>
  );
};

export default RunAutomationMenu;
