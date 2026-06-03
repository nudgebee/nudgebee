import { useCallback, useState } from 'react';

export interface ConfirmDialog<TAction extends string, TTarget> {
  /** True when a confirmation is pending. */
  isOpen: boolean;
  /** The pending action label (e.g. 'delete', 'disable', 'enable'). */
  action: TAction | null;
  /** The item the action will operate on. */
  target: TTarget | null;
  /** True while the user-supplied async action is in flight. */
  loading: boolean;
  /** Open the dialog for a specific action on a specific item. */
  openConfirm: (action: TAction, target: TTarget) => void;
  /** Close without running anything (Cancel button, backdrop click). */
  closeConfirm: () => void;
  /**
   * Run an async action under the dialog's loading state.
   *
   * Behavior:
   *  - fn() resolves → dialog closes, loading goes false.
   *  - fn() rejects → dialog STAYS OPEN, loading goes false, rejection
   *    propagates out to the caller (the try inside runAction doesn't
   *    catch). The "stays open on rejection" path lets consumers
   *    surface the error AND let the user retry without re-clicking
   *    the kebab — but only if fn() actually throws.
   *
   * Two valid consumer patterns:
   *  (a) Catch inside fn(), surface via snackbar, swallow → runAction
   *      sees resolution → dialog closes. User has to re-open to retry.
   *      `LLMConfigList` and `MCPConfigList` use this pattern today for
   *      uniform error handling.
   *  (b) Catch inside fn(), surface via snackbar, RE-THROW → runAction
   *      sees rejection → dialog stays open for retry. Use this when
   *      the action is expensive and re-opening is a friction point.
   */
  runAction: (fn: () => Promise<void>) => Promise<void>;
}

/**
 * Generic state holder for the "Are you sure?" dialog pattern used by the
 * integration list panels. Keeps `action`, `target`, and `loading` out of
 * every consumer and gives them a single async-runner helper.
 *
 * Used by `LLMConfigList` and `MCPConfigList` for delete / disable / enable
 * confirmations.
 */
export function useConfirmDialog<TAction extends string, TTarget>(): ConfirmDialog<TAction, TTarget> {
  const [action, setAction] = useState<TAction | null>(null);
  const [target, setTarget] = useState<TTarget | null>(null);
  const [loading, setLoading] = useState(false);

  const openConfirm = useCallback((nextAction: TAction, nextTarget: TTarget) => {
    setAction(nextAction);
    setTarget(nextTarget);
  }, []);

  const closeConfirm = useCallback(() => {
    setAction(null);
    setTarget(null);
  }, []);

  const runAction = useCallback(async (fn: () => Promise<void>) => {
    setLoading(true);
    try {
      await fn();
      // Close on success; on error, leave the dialog open so the caller
      // can surface the failure and let the user try again or cancel.
      setAction(null);
      setTarget(null);
    } finally {
      setLoading(false);
    }
  }, []);

  return {
    isOpen: action !== null,
    action,
    target,
    loading,
    openConfirm,
    closeConfirm,
    runAction,
  };
}
