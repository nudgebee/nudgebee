import { renderHook, act } from '@testing-library/react';
import useRecommendationExport from '@hooks/useRecommendationExport';

jest.mock('@api1/recommendation', () => ({
  __esModule: true,
  default: {
    exportRecommendations: jest.fn(),
  },
}));

jest.mock('@components1/common/snackbarService', () => ({
  snackbar: { success: jest.fn(), error: jest.fn() },
}));

jest.mock('src/utils/fileDownload', () => ({
  downloadBase64File: jest.fn(),
}));

import recommendationApi from '@api1/recommendation';
import { snackbar } from '@components1/common/snackbarService';
import { downloadBase64File } from 'src/utils/fileDownload';

const mockExport = recommendationApi.exportRecommendations;

describe('useRecommendationExport', () => {
  const defaultOptions = {
    accountId: 'acc-1',
    category: 'RightSizing',
    ruleName: 'pod_right_sizing',
  };

  beforeEach(() => jest.clearAllMocks());

  it('calls exportRecommendations with correct parameters for csv format', async () => {
    mockExport.mockResolvedValue({ data: { data: { recommendation_export: null } } });
    const { result } = renderHook(() => useRecommendationExport(defaultOptions));
    await act(async () => {
      await result.current.handleExportDownload('csv');
    });
    expect(mockExport).toHaveBeenCalledWith(expect.objectContaining({ accountId: 'acc-1', category: 'RightSizing', format: 'csv' }));
  });

  it('calls exportRecommendations with xlsx format when requested', async () => {
    mockExport.mockResolvedValue({ data: { data: { recommendation_export: null } } });
    const { result } = renderHook(() => useRecommendationExport(defaultOptions));
    await act(async () => {
      await result.current.handleExportDownload('xlsx');
    });
    expect(mockExport).toHaveBeenCalledWith(expect.objectContaining({ format: 'xlsx' }));
  });

  it('calls downloadBase64File and shows success snackbar on successful export', async () => {
    mockExport.mockResolvedValue({
      data: {
        data: {
          recommendation_export: {
            file_data: 'base64data==',
            filename: 'export.csv',
            content_type: 'text/csv',
          },
        },
      },
    });
    const { result } = renderHook(() => useRecommendationExport(defaultOptions));
    await act(async () => {
      await result.current.handleExportDownload('csv');
    });
    expect(downloadBase64File).toHaveBeenCalledWith('base64data==', 'export.csv', 'text/csv');
    expect(snackbar.success).toHaveBeenCalledWith('Export downloaded successfully');
  });

  it('shows error snackbar when API returns no data', async () => {
    mockExport.mockResolvedValue({ data: { data: { recommendation_export: null } } });
    const { result } = renderHook(() => useRecommendationExport(defaultOptions));
    await act(async () => {
      await result.current.handleExportDownload('csv');
    });
    expect(snackbar.error).toHaveBeenCalledWith('Export failed: No data received');
  });

  it('shows error snackbar on API exception', async () => {
    mockExport.mockRejectedValue(new Error('Connection refused'));
    const { result } = renderHook(() => useRecommendationExport(defaultOptions));
    await act(async () => {
      await result.current.handleExportDownload('csv');
    });
    expect(snackbar.error).toHaveBeenCalledWith('Export failed: Connection refused');
  });

  it('passes optional filters (namespace, workloadType, status) to API', async () => {
    mockExport.mockResolvedValue({ data: { data: { recommendation_export: null } } });
    const { result } = renderHook(() =>
      useRecommendationExport({ ...defaultOptions, namespace: 'default', workloadType: 'Deployment', status: 'open' })
    );
    await act(async () => {
      await result.current.handleExportDownload('csv');
    });
    expect(mockExport).toHaveBeenCalledWith(expect.objectContaining({ namespace: 'default', workloadType: 'Deployment', status: 'open' }));
  });
});
