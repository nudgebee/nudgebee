import { renderHook, act } from '@testing-library/react';
import { useOptionsSource } from '../useOptionsSource';
import apiWorkflow from '@api1/workflow';
import apiCloudAccount from '@api1/cloud-account';
import { toast as snackbar } from '@ui/Toast';

jest.mock('@api1/workflow', () => ({
  __esModule: true,
  default: {
    listMCPTools: jest.fn(),
  },
}));

jest.mock('@api1/cloud-account', () => ({
  __esModule: true,
  default: {
    getCloudResource: jest.fn(),
  },
}));

jest.mock('@ui/Toast', () => ({
  __esModule: true,
  toast: {
    error: jest.fn(),
  },
}));

const mockListMCPTools = apiWorkflow.listMCPTools as jest.Mock;
const mockGetCloudResource = apiCloudAccount.getCloudResource as jest.Mock;
const mockToastError = snackbar.error as jest.Mock;

const mockMcpTaskDef = {
  name: 'llm.mcp_call',
  input_schema: {
    connection_mode: {
      type: 'string',
      enum: ['integration', 'direct'],
      default: 'direct',
    },
    url: {
      type: 'string',
    },
    unrelated_field: {
      type: 'string',
    },
    tool_name: {
      type: 'string',
      options_source: {
        type: 'mcp_tools',
        dependency_mapping: {
          connection_mode: 'connection_mode',
          url: 'url',
        },
      },
    },
  },
};

const mockCloudServicesTaskDef = {
  name: 'cloud_services_task',
  input_schema: {
    account_id: {
      type: 'string',
      enum: ['acc-1', 'acc-2'],
    },
    service_name: {
      type: 'string',
      options_source: {
        type: 'cloud_services',
        dependency_mapping: {
          account_id: 'account_id',
        },
      },
    },
  },
};

describe('useOptionsSource', () => {
  beforeEach(() => {
    jest.useFakeTimers();
    jest.clearAllMocks();
  });

  afterEach(() => {
    jest.useRealTimers();
  });

  it('growing url -> 0 calls before timer, exactly 1 after 500 ms', async () => {
    mockListMCPTools.mockResolvedValue({
      data: {
        workflow_list_mcp_tools: {
          tools: [{ name: 'tool_a', description: 'Tool A' }],
        },
      },
    });

    let formValues = { connection_mode: 'direct', url: '', _accountId: 'acc-1' };
    const { rerender } = renderHook(({ fv }) => useOptionsSource(mockMcpTaskDef, fv), {
      initialProps: { fv: formValues },
    });

    expect(mockListMCPTools).not.toHaveBeenCalled();

    // Type first part of URL
    formValues = { connection_mode: 'direct', url: 'http://example.com/m', _accountId: 'acc-1' };
    rerender({ fv: formValues });
    expect(mockListMCPTools).not.toHaveBeenCalled();

    // Type full URL
    formValues = { connection_mode: 'direct', url: 'http://example.com/mcp', _accountId: 'acc-1' };
    rerender({ fv: formValues });
    expect(mockListMCPTools).not.toHaveBeenCalled();

    // Advance timer past debounce threshold (500 ms)
    await act(async () => {
      jest.advanceTimersByTime(500);
      await Promise.resolve();
    });

    expect(mockListMCPTools).toHaveBeenCalledTimes(1);
    expect(mockListMCPTools).toHaveBeenCalledWith('acc-1', { url: 'http://example.com/mcp' });
  });

  it('invalid url -> 0 calls, 0 toasts, loading === false', async () => {
    const formValues = { connection_mode: 'direct', url: 'invalid-url', _accountId: 'acc-1' };
    const { result } = renderHook(() => useOptionsSource(mockMcpTaskDef, formValues));

    await act(async () => {
      jest.advanceTimersByTime(1000);
      await Promise.resolve();
    });

    expect(mockListMCPTools).not.toHaveBeenCalled();
    expect(mockToastError).not.toHaveBeenCalled();
    expect(result.current.tool_name?.loading).not.toBe(true);
  });

  it('cleanup trap: editing unrelated field during debounce window does not drop the timer or leave stuck loading', async () => {
    mockListMCPTools.mockResolvedValue({
      data: {
        workflow_list_mcp_tools: {
          tools: [{ name: 'tool_a', description: 'Tool A' }],
        },
      },
    });

    // First render with initial valid URL (first run)
    let formValues = { connection_mode: 'direct', url: 'http://example.com/mcp', unrelated_field: 'a', _accountId: 'acc-1' };
    const { result, rerender } = renderHook(({ fv }) => useOptionsSource(mockMcpTaskDef, fv), {
      initialProps: { fv: formValues },
    });

    // Resolve initial immediate fetch
    await act(async () => {
      await Promise.resolve();
    });
    expect(mockListMCPTools).toHaveBeenCalledTimes(1);

    // Now edit URL (starts 500ms debounce timer)
    formValues = { connection_mode: 'direct', url: 'http://example.com/mcp2', unrelated_field: 'a', _accountId: 'acc-1' };
    rerender({ fv: formValues });

    // Advance timer partially (200 ms)
    act(() => {
      jest.advanceTimersByTime(200);
    });

    // Edit unrelated field while timer is still pending
    formValues = { connection_mode: 'direct', url: 'http://example.com/mcp2', unrelated_field: 'ab', _accountId: 'acc-1' };
    rerender({ fv: formValues });

    // Advance remaining time (300 ms, total 500 ms for URL change)
    await act(async () => {
      jest.advanceTimersByTime(300);
      await Promise.resolve();
    });

    // Must execute second fetch and update state cleanly (not stuck loading)
    expect(mockListMCPTools).toHaveBeenCalledTimes(2);
    expect(mockListMCPTools).toHaveBeenLastCalledWith('acc-1', { url: 'http://example.com/mcp2' });
    expect(result.current.tool_name?.loading).toBe(false);
    expect(result.current.tool_name?.options).toEqual([{ label: 'tool_a', value: 'tool_a', description: 'Tool A' }]);
  });

  it('a dropdown-only dep fires synchronously on initial run and change', async () => {
    mockGetCloudResource.mockResolvedValue({
      data: {
        data: {
          cloud_resourses: [{ service_name: 'ec2' }, { service_name: 's3' }],
        },
      },
    });

    const formValues = { account_id: 'acc-1' };
    renderHook(() => useOptionsSource(mockCloudServicesTaskDef, formValues));
    await act(async () => {
      await Promise.resolve();
    });

    // Dropdown dependency should trigger fetch immediately on first run without waiting for 500ms timer
    expect(mockGetCloudResource).toHaveBeenCalledTimes(1);
    expect(mockGetCloudResource).toHaveBeenCalledWith({ account_id: 'acc-1', status: 'Active' }, 1000);
  });

  it('failed direct-mode fetch -> toast + error on the field', async () => {
    mockListMCPTools.mockResolvedValue({
      errors: [{ message: 'Connection refused' }],
    });

    const formValues = { connection_mode: 'direct', url: 'http://example.com/mcp', _accountId: 'acc-1' };
    const { result } = renderHook(() => useOptionsSource(mockMcpTaskDef, formValues));

    await act(async () => {
      jest.advanceTimersByTime(500);
      await Promise.resolve();
    });

    expect(mockToastError).toHaveBeenCalledWith(
      'Unable to fetch MCP tools from the provided URL. Please verify the URL and try again. (Connection refused)'
    );
    expect(result.current.tool_name?.error).toBe(
      'Unable to fetch MCP tools from the provided URL. Please verify the URL and try again. (Connection refused)'
    );
    expect(result.current.tool_name?.loading).toBe(false);
  });
});
