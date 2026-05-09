import { useCallback } from 'react';
import recommendationApi from '@api1/recommendation';
import { snackbar } from '@components1/common/snackbarService';
import { downloadBase64File } from 'src/utils/fileDownload';

/**
 * Custom hook for handling recommendation exports
 * @param {Object} options - Export options
 * @param {string} options.accountId - The account ID
 * @param {string} options.category - The recommendation category (e.g., 'RightSizing', 'K8sSpotRecommendation')
 * @param {string} [options.ruleName] - The rule name (e.g., 'pod_right_sizing', 'replica_right_sizing')
 * @param {string} [options.namespace] - Optional namespace filter
 * @param {string} [options.workloadType] - Optional workload type filter
 * @param {string} [options.workloadName] - Optional workload name filter
 * @param {string|string[]} [options.status] - Optional status filter
 * @returns {Object} - Object containing handleExportDownload function
 */
const normalizeStatus = (status) => {
  if (Array.isArray(status) && status.length > 0) {
    return status;
  }
  if (typeof status === 'string' && status.trim() !== '') {
    return [status];
  }
  return undefined;
};

const useRecommendationExport = ({ accountId, category, ruleName, namespace, workloadType, workloadName, status }) => {
  const handleExportDownload = useCallback(
    async (format) => {
      try {
        const exportFormat = format === 'xlsx' ? 'xlsx' : 'csv';
        const response = await recommendationApi.exportRecommendations({
          accountId,
          category,
          ruleName,
          namespace: namespace || undefined,
          workloadType: workloadType || undefined,
          workloadName: workloadName || undefined,
          status: normalizeStatus(status),
          format: exportFormat,
        });

        if (response?.data?.data?.recommendation_export) {
          const { file_data, filename, content_type } = response.data.data.recommendation_export;
          downloadBase64File(file_data, filename, content_type);
          snackbar.success('Export downloaded successfully');
        } else {
          snackbar.error('Export failed: No data received');
        }
      } catch (error) {
        console.error('Export error:', error);
        snackbar.error(`Export failed: ${error.message}`);
      }
    },
    [accountId, category, ruleName, namespace, workloadType, workloadName, status]
  );

  return { handleExportDownload };
};

export default useRecommendationExport;
