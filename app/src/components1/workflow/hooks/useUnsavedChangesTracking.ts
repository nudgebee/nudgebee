import { useState, useEffect, useCallback, useRef } from 'react';
import { useRouter } from 'next/router';
import type { Node, Edge } from 'reactflow';
import type { WorkflowSettings, WorkflowStatus } from '@components1/workflow/types';

interface WorkflowData {
  id: string | null;
  name: string;
  status?: WorkflowStatus;
  definition: {
    version: string;
    timeout: string;
    inputs: any[];
    tasks: any[];
    triggers: Array<{ type: string; params: any }>;
    output: any;
    retry_policy: {
      maximum_attempts: number;
      initial_interval: string;
      maximum_interval: string;
      backoff_coefficient: number;
    };
  };
  tags: Record<string, any>;
}

interface UseUnsavedChangesTrackingProps {
  nodes: Node[];
  edges: Edge[];
  workflowSettings: WorkflowSettings;
  workflowData: WorkflowData | null;
  loading: boolean;
  /** Set to true after workflow data has been fully loaded and state is ready */
  isInitialized: boolean;
  /** When false, all tracking is disabled (e.g., read-only users who cannot mutate the workflow). Default true. */
  enabled?: boolean;
  /** Saved draft is ahead of the published live version (saved but not published). */
  hasUnpublishedChanges?: boolean;
  /** Called on navigation away when there are saved-but-unpublished changes (and no unsaved canvas edits). Non-blocking — a subtle reminder. */
  onUnpublishedExit?: () => void;
}

interface UseUnsavedChangesTrackingReturn {
  hasUnsavedChanges: boolean;
  showUnsavedChangesDialog: boolean;
  setShowUnsavedChangesDialog: React.Dispatch<React.SetStateAction<boolean>>;
  handleConfirmNavigation: () => void;
  handleCancelNavigation: () => void;
  updateSavedSnapshot: () => void;
  /** Temporarily pause change detection (e.g., during save-and-redirect flow) */
  pauseChangeDetection: () => void;
  /** Resume change detection and update snapshot with current state */
  resumeChangeDetection: () => void;
}

/**
 * Hook to track unsaved changes in the workflow builder and warn users
 * before navigating away with unsaved changes.
 *
 * Features:
 * - Tracks workflow state changes (nodes, edges, settings)
 * - Shows browser warning on page close/refresh
 * - Intercepts Next.js route changes with confirmation dialog
 * - Provides functions to confirm/cancel navigation
 */
export function useUnsavedChangesTracking({
  nodes,
  edges,
  workflowSettings,
  workflowData,
  loading,
  isInitialized,
  enabled = true,
  hasUnpublishedChanges = false,
  onUnpublishedExit,
}: UseUnsavedChangesTrackingProps): UseUnsavedChangesTrackingReturn {
  const router = useRouter();

  // Track unsaved changes state
  const [hasUnsavedChanges, setHasUnsavedChanges] = useState(false);
  const [savedStateSnapshot, setSavedStateSnapshot] = useState<string>('');
  const [showUnsavedChangesDialog, setShowUnsavedChangesDialog] = useState(false);
  const [pendingNavigation, setPendingNavigation] = useState<string | null>(null);

  // Ref to track if we should allow navigation (after user confirms)
  const allowNavigationRef = useRef(false);

  // Ref to pause change detection during save-and-redirect flow
  const changeDetectionPausedRef = useRef(false);

  // Ref to signal that snapshot should be updated on next render cycle
  const pendingSnapshotUpdateRef = useRef(false);

  // Ref to track whether the initial snapshot has been taken
  const initialSnapshotTakenRef = useRef(false);

  // Timestamp (ms) until which post-load state changes are treated as part of
  // initialization settling rather than user edits. The baseline snapshot is
  // captured the moment `isInitialized` flips, but some load-driven state
  // (notably workflowSettings synced from workflowData in a follow-up effect)
  // lands a render later. Without this window those late syncs would register
  // as spurious "unsaved changes". A user cannot meaningfully edit within a few
  // hundred ms of the page rendering, so re-baselining during the window is safe.
  const settleUntilRef = useRef(0);

  /**
   * Extract only the essential, user-editable fields from node data.
   * Excludes volatile fields like 'valid', 'errors', 'description' that can
   * differ after reload without representing actual user changes.
   */
  const extractEssentialNodeData = useCallback((data: any) => {
    if (!data) return {};

    // For trigger nodes, extract trigger configuration
    if (data.trigger) {
      return {
        triggerType: data.triggerType,
        trigger: data.trigger,
      };
    }

    // For action/task nodes, extract task configuration (excluding validation results)
    if (data.taskConfig) {
      const { valid: _valid, errors: _errors, ...essentialConfig } = data.taskConfig;
      return {
        taskConfig: essentialConfig,
      };
    }

    return {};
  }, []);

  /**
   * Generate a snapshot of the current workflow state for comparison.
   * Only includes relevant fields that indicate actual user changes.
   * Excludes volatile/computed fields that can differ after reload.
   */
  const generateWorkflowSnapshot = useCallback((): string => {
    const snapshot = {
      name: workflowData?.name || '',
      nodes: nodes.map((n) => ({
        id: n.id,
        type: n.type,
        data: extractEssentialNodeData(n.data),
      })),
      edges: edges.map((e) => ({
        id: e.id,
        source: e.source,
        target: e.target,
        sourceHandle: e.sourceHandle,
        targetHandle: e.targetHandle,
      })),
      settings: {
        timeout: workflowSettings.timeout,
        maxInterval: workflowSettings.maxInterval,
        retries: workflowSettings.retries,
        inputs: workflowSettings.inputs,
        outputs: workflowSettings.outputs,
        tags: workflowSettings.tags,
      },
    };
    return JSON.stringify(snapshot);
  }, [nodes, edges, workflowSettings, workflowData?.name, extractEssentialNodeData]);

  /**
   * Update the saved snapshot to the current state.
   * Call this after a successful save operation.
   */
  const updateSavedSnapshot = useCallback(() => {
    const snapshot = generateWorkflowSnapshot();
    setSavedStateSnapshot(snapshot);
    setHasUnsavedChanges(false);
  }, [generateWorkflowSnapshot]);

  /**
   * Temporarily pause change detection.
   * Use this before initiating save-and-redirect flows to prevent
   * false positive unsaved changes during state transitions.
   */
  const pauseChangeDetection = useCallback(() => {
    changeDetectionPausedRef.current = true;
    // Also allow navigation while paused
    allowNavigationRef.current = true;
    setHasUnsavedChanges(false);
  }, []);

  /**
   * Resume change detection and update snapshot with current state.
   * Call this after state has stabilized (e.g., after workflow reload completes).
   * Uses pendingSnapshotUpdateRef to defer snapshot generation until next render
   * when React state has been fully updated.
   */
  const resumeChangeDetection = useCallback(() => {
    changeDetectionPausedRef.current = false;
    allowNavigationRef.current = false;
    // Signal that we need to update snapshot on next render cycle
    // This ensures we capture the updated state after React batches the updates
    pendingSnapshotUpdateRef.current = true;
    setHasUnsavedChanges(false);
  }, []);

  /**
   * Handle user confirming they want to navigate away despite unsaved changes.
   */
  const handleConfirmNavigation = useCallback(() => {
    allowNavigationRef.current = true;
    setShowUnsavedChangesDialog(false);

    if (pendingNavigation) {
      router.push(pendingNavigation);
      setPendingNavigation(null);
    }
  }, [pendingNavigation, router]);

  /**
   * Handle user canceling navigation to stay on the page.
   */
  const handleCancelNavigation = useCallback(() => {
    setShowUnsavedChangesDialog(false);
    setPendingNavigation(null);
  }, []);

  // Initialize saved snapshot when workflow is fully loaded and not loading
  // Uses a ref to ensure this only happens once, avoiding re-snapshots on state changes
  useEffect(() => {
    if (!enabled) {
      return;
    }
    if (isInitialized && !loading && !initialSnapshotTakenRef.current) {
      initialSnapshotTakenRef.current = true;
      // Open a short settling window so load-driven follow-up state syncs
      // (e.g. workflowSettings derived from workflowData) re-baseline rather
      // than flag as edits. See settleUntilRef.
      settleUntilRef.current = Date.now() + 800;
      const snapshot = generateWorkflowSnapshot();
      setSavedStateSnapshot(snapshot);
    }
  }, [isInitialized, loading, generateWorkflowSnapshot, enabled]);

  // Handle pending snapshot update after state has stabilized
  // This runs after React has processed all state updates from resumeChangeDetection
  useEffect(() => {
    if (pendingSnapshotUpdateRef.current && !loading) {
      pendingSnapshotUpdateRef.current = false;
      const snapshot = generateWorkflowSnapshot();
      setSavedStateSnapshot(snapshot);
      setHasUnsavedChanges(false);
    }
  }, [nodes, edges, workflowSettings, workflowData?.name, loading, generateWorkflowSnapshot]);

  // Track changes by comparing current state to saved snapshot
  useEffect(() => {
    // Skip entirely when tracking is disabled (e.g., read-only user). Use a
    // functional setState so `hasUnsavedChanges` does not need to live in the
    // dependency array (which would re-run this effect on every change).
    if (!enabled) {
      setHasUnsavedChanges((prev) => (prev ? false : prev));
      return;
    }
    // Skip change detection if paused (during save-and-redirect flow)
    if (changeDetectionPausedRef.current) {
      return;
    }

    // Skip if we're waiting to update the snapshot (resumeChangeDetection was called)
    if (pendingSnapshotUpdateRef.current) {
      return;
    }

    if (!savedStateSnapshot || loading) {
      return;
    }

    const currentSnapshot = generateWorkflowSnapshot();
    const hasChanges = currentSnapshot !== savedStateSnapshot;

    // During the initial settling window, absorb load-driven state changes by
    // re-baselining instead of reporting them as unsaved edits.
    if (hasChanges && Date.now() < settleUntilRef.current) {
      setSavedStateSnapshot(currentSnapshot);
      setHasUnsavedChanges(false);
      return;
    }

    setHasUnsavedChanges(hasChanges);
  }, [nodes, edges, workflowSettings, workflowData?.name, savedStateSnapshot, loading, generateWorkflowSnapshot, enabled]);

  // Browser beforeunload handler - warns user before closing tab/window
  useEffect(() => {
    const handleBeforeUnload = (e: BeforeUnloadEvent) => {
      if (hasUnsavedChanges) {
        e.preventDefault();
        // eslint-disable-next-line deprecation/deprecation -- returnValue is deprecated but required for cross-browser beforeunload support
        e.returnValue = '';
        return '';
      }
    };

    window.addEventListener('beforeunload', handleBeforeUnload);
    return () => window.removeEventListener('beforeunload', handleBeforeUnload);
  }, [hasUnsavedChanges]);

  // Next.js route change handler - intercepts navigation attempts
  useEffect(() => {
    const handleRouteChangeStart = (url: string) => {
      // Allow navigation if user already confirmed or no unsaved canvas edits.
      if (allowNavigationRef.current || !hasUnsavedChanges) {
        const wasAllowed = allowNavigationRef.current;
        allowNavigationRef.current = false;
        // Subtle, non-blocking reminder: navigating away with saved-but-unpublished
        // changes. We don't block — just nudge so the user knows their edits aren't live.
        if (!wasAllowed && !hasUnsavedChanges && hasUnpublishedChanges) {
          onUnpublishedExit?.();
        }
        return;
      }

      // Block navigation and show confirmation dialog
      setPendingNavigation(url);
      setShowUnsavedChangesDialog(true);

      // Throw error to prevent navigation (Next.js pattern)
      router.events.emit('routeChangeError');
      throw 'Route change aborted due to unsaved changes. This is not an error.';
    };

    router.events.on('routeChangeStart', handleRouteChangeStart);
    return () => router.events.off('routeChangeStart', handleRouteChangeStart);
  }, [hasUnsavedChanges, hasUnpublishedChanges, onUnpublishedExit, router]);

  return {
    hasUnsavedChanges,
    showUnsavedChangesDialog,
    setShowUnsavedChangesDialog,
    handleConfirmNavigation,
    handleCancelNavigation,
    updateSavedSnapshot,
    pauseChangeDetection,
    resumeChangeDetection,
  };
}

export default useUnsavedChangesTracking;
