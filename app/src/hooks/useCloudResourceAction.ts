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

  // overrideArgs lets a bespoke dialog (e.g. RunSsmCommandDialog) pass the
  // collected form values directly, sidestepping the async setState gap
  // between setActionArgs() and executeAction() being called in the same tick.
  const executeAction = useCallback(
    async (overrideArgs?: Record<string, any>): Promise<{ success: boolean; message: string } | undefined> => {
      const act = state.selectedAction;
      const res = state.selectedResource;
      if (!act || !res || !params.accountId) {
        return undefined;
      }

      // Merge resource-derived args (e.g. ECS cluster name from row meta)
      // with user-supplied args. User-supplied wins on key conflicts.
      const baseArgs = act.extraArgs ? act.extraArgs(res) : {};
      const userArgs = overrideArgs ?? state.actionArgs;
      const argsToSend = { ...baseArgs, ...userArgs };
      const serviceName = act.serviceNameOverride ?? params.serviceName;

      setState((prev) => ({ ...prev, isLoading: true }));
      let result: { success: boolean; message: string } | undefined;
      try {
        result = await apiCloudAccount.applyCommand({
          account_id: params.accountId,
          service_name: serviceName,
          region: res.region,
          resource_id: res.resourse_id,
          command: act.command,
          args: argsToSend && Object.keys(argsToSend).length > 0 ? argsToSend : undefined,
        });

        // Actions that render their own result view (e.g. SSM Run Command) keep
        // the snackbar to a concise status — the full, possibly multi-line
        // command output is shown in their dedicated dialog instead.
        if (result?.success) {
          const detail = act.suppressResultMessage ? '' : result.message ? ': ' + result.message : '';
          snackbar.success(`${act.label} executed successfully${detail}`);
        } else if (act.suppressResultMessage) {
          snackbar.error(`${act.label} failed`);
        } else {
          snackbar.error(`${act.label} failed: ${result?.message || 'Unknown error'}`);
        }
      } catch (error: any) {
        const message = error?.message || 'Network error';
        snackbar.error(`${act.label} failed: ${message}`);
        result = { success: false, message };
      } finally {
        setState(initialState);
        const delay = params.refreshDelayMs ?? 3000;
        setTimeout(() => {
          params.onRefresh();
        }, delay);
      }
      return result;
    },
    [state.selectedAction, state.selectedResource, state.actionArgs, params]
  );

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
