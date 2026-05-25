import React from 'react';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';

jest.mock('@api1/cloud-account', () => ({
  __esModule: true,
  default: {
    listEvents: jest.fn(),
  },
}));

jest.mock('@lib/datetime', () => ({
  getLast7Days: () => new Date('2026-05-10T00:00:00Z'),
}));

jest.mock('@hooks/useTenantBranding', () => ({
  getBrandingAsset: () => '/helpbee.svg',
  useTenantBranding: () => ({}),
}));

jest.mock('@assets', () => ({
  TicketsIcon: '/tickets.svg',
}));

jest.mock('src/utils/actionStyles', () => ({
  action: { primary: {} },
}));

jest.mock('src/utils/colors', () => ({
  colors: { text: { primary: '#000' }, background: { white: '#fff' } },
}));

jest.mock('@components1/common/format/Text', () => ({
  __esModule: true,
  default: ({ value }: any) => <span>{value}</span>,
}));

jest.mock('@components1/common/NewCustomButton', () => ({
  __esModule: true,
  default: ({ text, onClick }: any) => (
    <button data-testid={`custom-btn-${text}`} onClick={onClick}>
      {text}
    </button>
  ),
}));

jest.mock('@components1/helpbee', () => ({
  __esModule: true,
  default: ({ isModalVisible, onClose }: any) =>
    isModalVisible ? (
      <div data-testid='helpbee-modal'>
        <button data-testid='helpbee-close' onClick={onClose}>
          close
        </button>
      </div>
    ) : null,
}));

jest.mock('@components1/common/ThreeDotsMenu', () => ({
  __esModule: true,
  default: ({ menuItems, data, onMenuClick }: any) => (
    <div data-testid='three-dots'>
      {(menuItems || []).map((mi: any) => (
        <button key={mi.id} data-testid={`menu-${mi.label}`} onClick={() => onMenuClick(mi, data)}>
          {mi.label}
        </button>
      ))}
    </div>
  ),
}));

jest.mock('@common/BoxLayout2', () => ({
  __esModule: true,
  default: ({ children, filterOptions = [], dateTimeRange, heading }: any) => (
    <div data-testid='box-layout'>
      <h2>{heading}</h2>
      <div data-testid='filters'>
        {filterOptions.map((f: any, i: number) => {
          if (f.type === 'textfield') {
            return (
              <input
                key={i}
                data-testid={`textfield-${f.id}`}
                value={f.value || ''}
                onChange={f.onChange}
                onKeyDown={f.onKeyDown}
                data-error={f.error ? 'true' : 'false'}
                data-helpertext={f.helperText || ''}
              />
            );
          }
          if (f.type === 'custom') {
            return <div key={i}>{f.component}</div>;
          }
          return null;
        })}
      </div>
      {dateTimeRange?.enabled && <div data-testid='date-range-enabled'>dr</div>}
      {children}
    </div>
  ),
}));

jest.mock('@components1/cloudaccount/CloudAccountTable', () => ({
  __esModule: true,
  default: ({ id, data, totalRows, loading, pageNumber, onPageChange }: any) => (
    <div data-testid='cloud-account-table' id={id}>
      {loading && <div data-testid='loading'>loading</div>}
      <div data-testid='total'>{totalRows}</div>
      <div data-testid='page'>{pageNumber}</div>
      {(data || []).map((row: any, i: number) => (
        <div key={i} data-testid={`row-${i}`}>
          {row.map((cell: any, j: number) => (
            <span key={j} data-testid={`cell-${i}-${j}`}>
              {cell.component}
            </span>
          ))}
        </div>
      ))}
      <button data-testid='next-page' onClick={() => onPageChange(2)}>
        Next
      </button>
    </div>
  ),
}));

import CloudAccountLogs from '@components1/cloudaccount/CloudAccountLogs';

const apiCloudAccount = require('@api1/cloud-account').default;

const sampleEvents = [
  {
    title: 'Login attempt',
    aggregation_key: 'iam:login',
    starts_at: '2026-05-15T10:00:00Z',
  },
  {
    title: 'Bucket access',
    aggregation_key: 's3:GetObject',
    starts_at: '2026-05-15T11:00:00Z',
  },
];

const mockResponse = (events = sampleEvents, count?: number) => ({
  data: {
    events,
    events_aggregate: { aggregate: { count: count ?? events.length } },
  },
});

describe('CloudAccountLogs (integration)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    apiCloudAccount.listEvents.mockResolvedValue(mockResponse());
  });

  it('does not fetch when accountId is missing', async () => {
    render(<CloudAccountLogs accountId={undefined} serviceName={undefined} />);
    await act(async () => {});
    expect(apiCloudAccount.listEvents).not.toHaveBeenCalled();
  });

  it('fetches logs on mount with accountId + serviceName + default pagination', async () => {
    render(<CloudAccountLogs accountId='acc-1' serviceName='ec2' />);

    await waitFor(() => expect(apiCloudAccount.listEvents).toHaveBeenCalled());
    const [params, limit, offset] = apiCloudAccount.listEvents.mock.calls[0];
    expect(params).toEqual({ accountId: 'acc-1', subjectNamespace: 'ec2' });
    expect(limit).toBe(10);
    expect(offset).toBe(0);
  });

  it('renders log rows with aggregation_key in row cells', async () => {
    render(<CloudAccountLogs accountId='acc-1' serviceName='ec2' />);

    await waitFor(() => expect(screen.getByText('iam:login')).toBeInTheDocument());
    expect(screen.getByText('s3:GetObject')).toBeInTheDocument();
  });

  it('shows Logstash Query + Index textfields with initial defaults', async () => {
    render(<CloudAccountLogs accountId='acc-1' serviceName='ec2' />);

    await waitFor(() => expect(screen.getByTestId('textfield-logstashQuery')).toBeInTheDocument());
    expect(screen.getByTestId('textfield-logstashQuery')).toHaveValue('{"match_all": {}}');
    expect(screen.getByTestId('textfield-index')).toHaveValue('logs-*-*');
  });

  it('flags Invalid JSON when submit pressed with bad query', async () => {
    render(<CloudAccountLogs accountId='acc-1' serviceName='ec2' />);
    await waitFor(() => expect(screen.getByTestId('textfield-logstashQuery')).toBeInTheDocument());

    fireEvent.change(screen.getByTestId('textfield-logstashQuery'), {
      target: { value: '{ not valid json' },
    });
    fireEvent.click(screen.getByTestId('custom-btn-Submit'));

    await waitFor(() => expect(screen.getByTestId('textfield-logstashQuery').getAttribute('data-error')).toBe('true'));
    expect(screen.getByTestId('textfield-logstashQuery').getAttribute('data-helpertext')).toBe('Invalid JSON');
  });

  it('clears error when typing after invalid submit', async () => {
    render(<CloudAccountLogs accountId='acc-1' serviceName='ec2' />);
    await waitFor(() => expect(screen.getByTestId('textfield-logstashQuery')).toBeInTheDocument());

    fireEvent.change(screen.getByTestId('textfield-logstashQuery'), {
      target: { value: 'bad' },
    });
    fireEvent.click(screen.getByTestId('custom-btn-Submit'));
    await waitFor(() => expect(screen.getByTestId('textfield-logstashQuery').getAttribute('data-error')).toBe('true'));

    fireEvent.change(screen.getByTestId('textfield-logstashQuery'), {
      target: { value: '{"match_all": {}}' },
    });

    expect(screen.getByTestId('textfield-logstashQuery').getAttribute('data-error')).toBe('false');
  });

  it('does not flag error when submit pressed with valid JSON', async () => {
    render(<CloudAccountLogs accountId='acc-1' serviceName='ec2' />);
    await waitFor(() => expect(screen.getByTestId('textfield-logstashQuery')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('custom-btn-Submit'));

    expect(screen.getByTestId('textfield-logstashQuery').getAttribute('data-error')).toBe('false');
    expect(screen.getByTestId('textfield-logstashQuery').getAttribute('data-helpertext')).toBe('');
  });

  it('triggers submit on Enter key in query field', async () => {
    render(<CloudAccountLogs accountId='acc-1' serviceName='ec2' />);
    await waitFor(() => expect(screen.getByTestId('textfield-logstashQuery')).toBeInTheDocument());

    fireEvent.change(screen.getByTestId('textfield-logstashQuery'), {
      target: { value: 'bad' },
    });
    fireEvent.keyDown(screen.getByTestId('textfield-logstashQuery'), { key: 'Enter' });

    await waitFor(() => expect(screen.getByTestId('textfield-logstashQuery').getAttribute('data-error')).toBe('true'));
  });

  it('paginates and updates offset on next page', async () => {
    render(<CloudAccountLogs accountId='acc-1' serviceName='ec2' />);
    await waitFor(() => expect(apiCloudAccount.listEvents).toHaveBeenCalled());
    apiCloudAccount.listEvents.mockClear();

    fireEvent.click(screen.getByTestId('next-page'));

    await waitFor(() => expect(apiCloudAccount.listEvents).toHaveBeenCalled());
    expect(apiCloudAccount.listEvents.mock.calls[0][2]).toBe(10);
  });

  it('opens HelpBee modal when menu HelpBee clicked', async () => {
    render(<CloudAccountLogs accountId='acc-1' serviceName='ec2' />);
    await waitFor(() => expect(screen.getAllByTestId('menu-HelpBee').length).toBeGreaterThan(0));

    fireEvent.click(screen.getAllByTestId('menu-HelpBee')[0]);

    expect(screen.getByTestId('helpbee-modal')).toBeInTheDocument();
  });

  it('closes HelpBee modal on close', async () => {
    render(<CloudAccountLogs accountId='acc-1' serviceName='ec2' />);
    await waitFor(() => expect(screen.getAllByTestId('menu-HelpBee').length).toBeGreaterThan(0));
    fireEvent.click(screen.getAllByTestId('menu-HelpBee')[0]);

    fireEvent.click(screen.getByTestId('helpbee-close'));

    expect(screen.queryByTestId('helpbee-modal')).not.toBeInTheDocument();
  });

  it('does not open HelpBee for Create Ticket menu (id=0)', async () => {
    render(<CloudAccountLogs accountId='acc-1' serviceName='ec2' />);
    await waitFor(() => expect(screen.getAllByTestId('menu-Create Ticket').length).toBeGreaterThan(0));

    fireEvent.click(screen.getAllByTestId('menu-Create Ticket')[0]);

    expect(screen.queryByTestId('helpbee-modal')).not.toBeInTheDocument();
  });

  it('enables date-time range filter', async () => {
    render(<CloudAccountLogs accountId='acc-1' serviceName='ec2' />);
    await waitFor(() => expect(screen.getByTestId('date-range-enabled')).toBeInTheDocument());
  });

  it('shows loading during fetch and clears after', async () => {
    let resolveFn: any;
    apiCloudAccount.listEvents.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveFn = resolve;
      })
    );

    render(<CloudAccountLogs accountId='acc-1' serviceName='ec2' />);
    expect(screen.getByTestId('loading')).toBeInTheDocument();

    await act(async () => {
      resolveFn(mockResponse([]));
    });

    await waitFor(() => expect(screen.queryByTestId('loading')).not.toBeInTheDocument());
  });

  it('handles API rejection without crashing (loading clears)', async () => {
    apiCloudAccount.listEvents.mockRejectedValue(new Error('boom'));

    render(<CloudAccountLogs accountId='acc-1' serviceName='ec2' />);

    await waitFor(() => expect(screen.queryByTestId('loading')).not.toBeInTheDocument());
  });

  it('handles empty events list gracefully', async () => {
    apiCloudAccount.listEvents.mockResolvedValue(mockResponse([], 0));

    render(<CloudAccountLogs accountId='acc-1' serviceName='ec2' />);

    await waitFor(() => expect(screen.getByTestId('total')).toHaveTextContent('0'));
    expect(screen.queryByTestId('row-0')).not.toBeInTheDocument();
  });
});
