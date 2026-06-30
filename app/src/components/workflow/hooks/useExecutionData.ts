import { useState, useEffect } from 'react';
import apiWorkflow from '@api1/workflow';
import { parseHttpResponseBodyMessage } from 'src/utils/common';

interface UseExecutionDataProps {
  workflowId: string;
  accountId: string;
  currentMode: 'editor' | 'json' | 'executions';
  workflowDataId?: string;
  // Optional filters
  status?: string;
  triggerType?: string;
}

export const useExecutionData = ({ workflowId, accountId, currentMode, workflowDataId, status, triggerType }: UseExecutionDataProps) => {
  const [executionData, setExecutionData] = useState<any[]>([]);
  const [executionLoading, setExecutionLoading] = useState(false);
  const [selectedExecution, setSelectedExecution] = useState<any | null>(null);
  const [executionSidebarOpen, setExecutionSidebarOpen] = useState(false);

  // Pagination state
  const [nextPageToken, setNextPageToken] = useState<string | null>(null);
  const [previousPageTokens, setPreviousPageTokens] = useState<string[]>([]);
  const [hasMore, setHasMore] = useState(true);
  const [hasPrevious, setHasPrevious] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);

  const fetchExecutionData = async (reset = true, pageToken?: string) => {
    try {
      if (reset) {
        setExecutionLoading(true);
        setNextPageToken(null);
        setPreviousPageTokens([]);
        setHasMore(true);
        setHasPrevious(false);
      } else if (pageToken) {
        setLoadingMore(true);
      }

      const response: any = await apiWorkflow.ListWorkflowExecutions(
        accountId,
        workflowId,
        10, // Default limit of 10
        pageToken,
        status,
        triggerType
      );

      const errorMessage = parseHttpResponseBodyMessage(response);
      if (errorMessage) {
        console.error('Failed to fetch workflow executions:', errorMessage);
        if (reset) {
          setExecutionData([]);
        }
      } else {
        const executions = response.data?.workflow_list_executions?.executions || [];
        const newNextPageToken = response.data?.workflow_list_executions?.next_page_token;

        // Always replace the data (no appending for next/previous)
        setExecutionData(executions);
        setNextPageToken(newNextPageToken);
        setHasMore(!!newNextPageToken && executions.length > 0);
      }
    } catch (error) {
      console.error('Failed to fetch execution data:', error);
      if (reset) {
        setExecutionData([]);
      }
    } finally {
      if (reset) {
        setExecutionLoading(false);
      } else if (pageToken) {
        setLoadingMore(false);
      }
    }
  };

  // Navigate to next page
  const goToNextPage = () => {
    if (!loadingMore && hasMore && nextPageToken) {
      // Store current position for going back
      setPreviousPageTokens((prev) => [...prev, nextPageToken]);
      setHasPrevious(true);
      fetchExecutionData(false, nextPageToken);
    }
  };

  // Navigate to previous page
  const goToPreviousPage = () => {
    if (!loadingMore && hasPrevious && previousPageTokens.length > 0) {
      // The token for the page we are going back to is the one before the last one in the history.
      // If there's only one token, we are going back to the first page (token is undefined).
      const tokenToUse = previousPageTokens.length > 1 ? previousPageTokens[previousPageTokens.length - 2] : undefined;

      // Update the token history by removing the last entry.
      const newPreviousTokens = previousPageTokens.slice(0, -1);
      setPreviousPageTokens(newPreviousTokens);
      setHasPrevious(newPreviousTokens.length > 0);

      fetchExecutionData(false, tokenToUse);
    }
  };

  // Fetch execution data when switching to executions mode
  useEffect(() => {
    if (currentMode === 'executions' && workflowDataId) {
      setExecutionSidebarOpen(true);
      fetchExecutionData();
    } else {
      setExecutionSidebarOpen(false);
      setSelectedExecution(null);
    }
  }, [currentMode, workflowDataId, status, triggerType]);

  // Auto-select the latest execution when execution data is first loaded
  useEffect(() => {
    if (currentMode === 'executions' && executionData.length > 0 && !selectedExecution) {
      setSelectedExecution(executionData[0]);
    }
  }, [executionData, currentMode, selectedExecution]);

  const handleExecutionSelect = (execution: any) => {
    setSelectedExecution(execution);
  };

  const handleCloseExecutionsSidebar = () => {
    setExecutionSidebarOpen(false);
    setSelectedExecution(null);
  };

  return {
    executionData,
    executionLoading,
    selectedExecution,
    executionSidebarOpen,
    fetchExecutionData,
    handleExecutionSelect,
    handleCloseExecutionsSidebar,
    // Pagination properties
    hasMore,
    hasPrevious,
    loadingMore,
    goToNextPage,
    goToPreviousPage,
  };
};
