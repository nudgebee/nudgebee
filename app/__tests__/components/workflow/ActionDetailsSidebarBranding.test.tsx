import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import ActionDetailsSidebar from '@components/workflow/ActionDetailsSidebar';

// Verbatim events.store description from runbook-server, which has no tenant branding.
const TASK_DEFINITIONS = [
  {
    name: 'events.store',
    description: 'Save data to Nudgebee for later troubleshooting and analysis.',
    input_schema: {
      event: { default: null, description: 'Event To Store', is_encrypted: false, order: 1, required: true, title: 'Event', type: 'string' },
    },
    output_schema: {},
  },
];

jest.mock('@hooks/useTenantBranding', () => ({
  ...jest.requireActual('@hooks/useTenantBranding'),
  useBrandingConfig: () => ({ title: 'Acme', assistantName: 'nubi', isWhiteLabel: true, loading: false }),
}));
jest.mock('@shared/viewers/MarkDowns', () => ({
  __esModule: true,
  default: ({ data }: { data: string }) => <div data-testid='markdown'>{data}</div>,
}));
jest.mock('@api1/workflow', () => ({ __esModule: true, default: { listConfigs: jest.fn().mockResolvedValue({ data: {} }) } }));
jest.mock('@api1/account', () => ({
  __esModule: true,
  default: {
    getDefaultProvider: jest.fn().mockResolvedValue({ data: { data: {} } }),
    getNotificationChannelList: jest.fn().mockResolvedValue({ data: { data: [] } }),
    getNotificationUserList: jest.fn().mockResolvedValue({ data: { data: [] } }),
  },
}));
jest.mock('@lib/auth', () => ({ isTenantAdmin: () => false, hasWriteAccess: () => true }));

describe('ActionDetailsSidebar task description branding', () => {
  it('shows the tenant brand instead of Nudgebee in the task description', async () => {
    render(
      <ActionDetailsSidebar
        variant='inline'
        open
        onClose={jest.fn()}
        selectedActionType='events.store'
        nodes={[]}
        edges={[]}
        onTaskDataChange={jest.fn()}
        taskDefinitions={TASK_DEFINITIONS as any}
        taskData={{}}
      />
    );

    await waitFor(() => expect(screen.getByText('Save data to Acme for later troubleshooting and analysis.')).toBeInTheDocument());
    expect(screen.queryByText(/Nudgebee/)).not.toBeInTheDocument();
  });
});
