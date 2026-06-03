import { useState, useCallback } from 'react';
import apiKubernetes1 from '@api1/kubernetes1';
import { snackbar } from '@components1/common/snackbarService';

/**
 * Custom hook for triggering anomaly detection
 * @param {string} accountId - The account ID
 * @returns {Object} - Object containing triggerAnomaly function and isLoading state
 */
const useTriggerAnomaly = (accountId: string) => {
  const [isLoading, setIsLoading] = useState(false);

  const triggerAnomaly = useCallback(async () => {
    setIsLoading(true);
    try {
      const res = await apiKubernetes1.triggerAnomalyExecute(accountId);
      if (res?.data?.data?.anomaly_execute?.status === 'triggered') {
        snackbar.success('Anomaly detection triggered successfully');
      } else if (res?.data?.errors) {
        snackbar.error('Failed to trigger anomaly detection');
      } else {
        snackbar.success(res?.data?.data?.anomaly_execute?.message || 'Anomaly detection triggered successfully');
      }
    } catch (error) {
      console.error('Trigger anomaly error:', error);
      snackbar.error('Failed to trigger anomaly detection');
    } finally {
      setIsLoading(false);
    }
  }, [accountId]);

  return { triggerAnomaly, isLoading };
};

export default useTriggerAnomaly;
