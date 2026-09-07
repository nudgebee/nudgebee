import React from 'react';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';
import KubernetesCreateAlert from '@components/k8s/details/KubernetesCreateAlert';

jest.mock('src/utils/colors');

jest.mock('@lib/auth', () => ({
  hasFeatureAccess: jest.fn().mockResolvedValue(false),
}));
jest.mock('@uiw/react-codemirror', () => ({
  __esModule: true,
  default: ({ value }: { value: string }) => <textarea aria-label='query' value={value} readOnly />,
}));
jest.mock('@prometheus-io/codemirror-promql', () => ({
  PromQLExtension: class {
    asExtension() {
      return [];
    }
  },
}));
jest.mock('@shared/forms/DynamicForm', () => ({
  __esModule: true,
  default: () => <div data-testid='dynamic-form' />,
}));
jest.mock('@shared/ReorderableList', () => ({
  __esModule: true,
  default: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
}));
// The stepper only has to hand the final submit through: the guard under test
// sits in front of everything the real stepper would validate.
jest.mock('@shared/navigation/CustomStepper', () => ({
  __esModule: true,
  default: ({ children, onSubmit, submitButtonText }: any) => (
    <div>
      {children}
      <button data-testid='stepper-submit' onClick={onSubmit}>
        {submitButtonText}
      </button>
    </div>
  ),
}));
jest.mock('@ui/Input', () => ({
  Input: ({ label, value, onChange }: any) => <input aria-label={label} value={value || ''} onChange={(e) => onChange?.(e.target.value)} />,
}));
jest.mock('@ui/Button', () => ({
  Button: ({ children, onClick }: any) => <button onClick={onClick}>{children}</button>,
}));
jest.mock('@ui/FilterDropdown', () => ({
  __esModule: true,
  default: ({ label }: any) => <select aria-label={label} />,
}));
jest.mock('@ui/Select', () => ({
  Select: ({ label }: any) => <select aria-label={label} />,
}));
jest.mock('@ui/Accordion', () => ({
  Accordion: ({ children }: any) => <div>{children}</div>,
}));
jest.mock('@ui/ProgressLinear', () => ({
  ProgressLinear: () => <div data-testid='progress' />,
}));
const mockToastError = jest.fn();
jest.mock('@ui/Toast', () => ({
  toast: { success: jest.fn(), error: (...args: any[]) => mockToastError(...args) },
}));

const mockCreateAlertManager = jest.fn();
jest.mock('@api1/kubernetes1', () => ({
  __esModule: true,
  default: {
    createAlertManager: (...args: any[]) => mockCreateAlertManager(...args),
    updateAlertManager: jest.fn(),
    getAgentPlaybookOfEvent: jest.fn().mockResolvedValue({ data: { data: { agent_playbook: [] } } }),
  },
}));
jest.mock('@api1/ask-nudgebee', () => ({
  __esModule: true,
  default: { listFunctions: jest.fn().mockResolvedValue({ res: { llm_functions: [] } }) },
}));
jest.mock('@api1/observability', () => ({ __esModule: true, default: {} }));

const mockGetDefaultProvider = jest.fn();
jest.mock('@api1/account', () => ({
  __esModule: true,
  default: { getDefaultProvider: (...args: any[]) => mockGetDefaultProvider(...args) },
}));

const providerResponse = (capabilities: Record<string, unknown>) => ({
  data: {
    data: {
      observability_get_default_provider: {
        provider: 'prometheus',
        integration_source: 'user',
        capabilities,
      },
    },
  },
});

const renderCreate = () =>
  render(
    <KubernetesCreateAlert
      accountId='acc-1'
      isCreateAlert
      handleCloseCreateNewAlertModal={jest.fn()}
      onSubmit={jest.fn()}
      onClickLoader={jest.fn()}
      alertManagerObject={null}
      agentPlaybookOnEvent={[]}
    />
  );

describe('KubernetesCreateAlert — direct (agentless) Prometheus', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  // A Prometheus connected without an agent and without a ruler cannot take a
  // rule: the form says so before anything is typed, and a submit never leaves
  // the browser.
  test('explains why rules cannot be created and blocks submit', async () => {
    mockGetDefaultProvider.mockResolvedValue(
      providerResponse({ supports_alert_rules: false, alert_rules_reason: 'this Prometheus integration has no rule-management API configured' })
    );

    await act(async () => {
      renderCreate();
    });

    await waitFor(() => {
      expect(screen.getByTestId('alert-rules-unavailable')).toBeInTheDocument();
    });
    expect(screen.getByTestId('alert-rules-unavailable')).toHaveTextContent('no rule-management API configured');

    await act(async () => {
      fireEvent.click(screen.getByTestId('stepper-submit'));
    });
    expect(mockToastError).toHaveBeenCalledWith(expect.stringContaining('no rule-management API configured'));
    expect(mockCreateAlertManager).not.toHaveBeenCalled();
  });

  test('shows no warning when the provider can take rules', async () => {
    mockGetDefaultProvider.mockResolvedValue(providerResponse({ supports_alert_rules: true }));

    await act(async () => {
      renderCreate();
    });

    await waitFor(() => {
      expect(mockGetDefaultProvider).toHaveBeenCalledWith({ account_id: 'acc-1', provider_type: 'metrics' });
    });
    expect(screen.queryByTestId('alert-rules-unavailable')).not.toBeInTheDocument();
  });

  // An older backend answers without the capability flag; that must not start
  // blocking every account.
  test('treats a missing capability flag as supported', async () => {
    mockGetDefaultProvider.mockResolvedValue(providerResponse({}));

    await act(async () => {
      renderCreate();
    });

    await waitFor(() => {
      expect(mockGetDefaultProvider).toHaveBeenCalled();
    });
    expect(screen.queryByTestId('alert-rules-unavailable')).not.toBeInTheDocument();
  });
});
