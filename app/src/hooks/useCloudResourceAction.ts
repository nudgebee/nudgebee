import { useState, useCallback } from 'react';
import apiCloudAccount from '@api1/cloud-account';
import { snackbar } from '@components1/common/snackbarService';
import type { ResourceAction } from '@components1/cloudaccount/resourceActions';

interface ActionState {
  isConfirmOpen: boolean;
  isLoading: boolean;
  selectedAction: ResourceAction | null;
  selectedResource: any | null;
  actionArgs: Record<string, any>;
  confirmInput: string;
}

const initialState: ActionState = {
  isConfirmOpen: false,
  isLoading: false,
  selectedAction: null,
  selectedResource: null,
  actionArgs: {},
  confirmInput: '',
};

export function useCloudResourceAction(params: {
  accountId: string | undefined;
  serviceName: string;
  onRefresh: () => void;
  refreshDelayMs?: number;
}) {
  const [state, setState] = useState<ActionState>(initialState);

  const initiateAction = useCallback((action: ResourceAction, resource: any) => {
    setState({
      isConfirmOpen: true,
      isLoading: false,
      selectedAction: action,
      selectedResource: resource,
      actionArgs: {},
      confirmInput: '',
    });
  }, []);

  const setActionArgs = useCallback((args: Record<string, any>) => {
    setState((prev) => ({ ...prev, actionArgs: args }));
  }, []);

  const setConfirmInput = useCallback((input: string) => {
    setState((prev) => ({ ...prev, confirmInput: input }));
  }, []);

  const closeConfirm = useCallback(() => {
    setState(initialState);
  }, []);

  const executeAction = useCallback(async () => {
    const act = state.selectedAction;
    const res = state.selectedResource;
    if (!act || !res || !params.accountId) {
      return;
    }

    setState((prev) => ({ ...prev, isLoading: true }));
    try {
      const result = await apiCloudAccount.applyCommand({
        account_id: params.accountId,
        service_name: params.serviceName,
        region: res.region,
        resource_id: res.resourse_id,
        command: act.command,
        args: Object.keys(state.actionArgs).length > 0 ? state.actionArgs : undefined,
      });

      if (result?.success) {
        snackbar.success(`${act.label} executed successfully${result.message ? ': ' + result.message : ''}`);
      } else {
        snackbar.error(`${act.label} failed: ${result?.message || 'Unknown error'}`);
      }
    } catch (error: any) {
      snackbar.error(`${act.label} failed: ${error?.message || 'Network error'}`);
    } finally {
      setState(initialState);
      const delay = params.refreshDelayMs ?? 3000;
      setTimeout(() => {
        params.onRefresh();
      }, delay);
    }
  }, [state.selectedAction, state.selectedResource, state.actionArgs, params]);

  const isStrictConfirmValid =
    state.selectedAction?.confirmationType === 'strict'
      ? state.confirmInput === (state.selectedResource?.name || state.selectedResource?.resourse_id)
      : true;

  return {
    isConfirmOpen: state.isConfirmOpen,
    isLoading: state.isLoading,
    selectedAction: state.selectedAction,
    selectedResource: state.selectedResource,
    actionArgs: state.actionArgs,
    confirmInput: state.confirmInput,
    initiateAction,
    setActionArgs,
    setConfirmInput,
    closeConfirm,
    executeAction,
    isStrictConfirmValid,
  };
}
