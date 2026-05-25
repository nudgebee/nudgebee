import { useMemo } from 'react';
import type { Node } from 'reactflow';

interface TaskConfig {
  if?: string;
  timeout?: string;
  set_state?: Record<string, unknown>;
  set_vars?: Record<string, unknown>;
  matrix?: Record<string, unknown>;
  hooks?: Record<string, unknown>;
  failure_policy?: Record<string, unknown>;
  disabled?: boolean;
  _prev_edges?: unknown;
}

interface UseSelectedNodeConfigResult {
  selectedNode: Node | undefined;
  taskConfig: TaskConfig;
  getConfigValue: <T>(field: keyof TaskConfig) => T | undefined;
  hasAnyAdvancedConfig: boolean;
}

/**
 * Hook to access the selected action node's configuration
 * Centralizes the repeated node selection logic used across advanced config fields
 */
export const useSelectedNodeConfig = (nodes: Node[]): UseSelectedNodeConfigResult => {
  const selectedNode = useMemo(() => {
    return nodes.find((node) => node.selected && node.type === 'action');
  }, [nodes]);

  const taskConfig = useMemo(() => {
    return (selectedNode?.data?.taskConfig as TaskConfig) || {};
  }, [selectedNode]);

  const getConfigValue = <T>(field: keyof TaskConfig): T | undefined => {
    return taskConfig[field] as T | undefined;
  };

  const hasAnyAdvancedConfig = useMemo(() => {
    const tc = taskConfig;
    return !!(tc.if || tc.timeout || tc.set_state || tc.set_vars || tc.matrix || tc.hooks || tc.failure_policy);
  }, [taskConfig]);

  return {
    selectedNode,
    taskConfig,
    getConfigValue,
    hasAnyAdvancedConfig,
  };
};

export default useSelectedNodeConfig;
