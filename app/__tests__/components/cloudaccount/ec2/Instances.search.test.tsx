import React from 'react';
import { render, fireEvent, waitFor } from '@testing-library/react';

// The real API module runs, so the assertion is on the query that leaves the
// browser, whichever layer does the trimming.
jest.mock('@lib/HttpService', () => ({
  queryGraphQL: jest.fn(),
  gqlStringify: jest.requireActual('@lib/HttpService').gqlStringify,
}));

jest.mock('@utils/colors');
jest.mock('@lib/auth', () => ({ hasWriteAccess: () => false }));
jest.mock('@hooks/useCloudResourceAction', () => ({ useCloudResourceAction: () => ({}) }));

jest.mock('@components/cloudaccount/ec2/Summary', () => ({ OptimizeSummary: () => null }));
jest.mock('@components/cloudaccount/CloudAccountTable', () => ({ __esModule: true, default: () => null }));
jest.mock('@components/cloudaccount/CloudAccountEvents', () => ({ __esModule: true, default: () => null }));
jest.mock('@components/cloudaccount/ServiceRefreshButton', () => ({ __esModule: true, default: () => null }));
jest.mock('@components/cloudaccount/ConfirmActionDialog', () => ({ __esModule: true, default: () => null }));
jest.mock('@components/cloudaccount/RunSsmCommandDialog', () => ({ __esModule: true, default: () => null }));
jest.mock('@components/cloudaccount/SsmCommandResultDialog', () => ({ __esModule: true, default: () => null }));
jest.mock('@components/cloudaccount/ResourceActionHistory', () => ({ __esModule: true, default: () => null }));
jest.mock('@shared/tables/CustomTable', () => ({ __esModule: true, default: () => null }));
jest.mock('@shared/buttons/DownloadButton', () => ({ __esModule: true, default: () => null }));
jest.mock('@ui/FilterDropdown', () => ({ __esModule: true, default: () => null }));

import InstancesView from '@components/cloudaccount/ec2/Instances';

const { queryGraphQL } = require('@lib/HttpService');

const searchBox = (container: HTMLElement) => container.querySelector('#ec2-instances-search') as HTMLInputElement;

const listQueries = () => queryGraphQL.mock.calls.filter((call: any[]) => call[1] === 'ListCloudResources').map((call: any[]) => call[0] as string);

describe('EC2 Instances search', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    queryGraphQL.mockResolvedValue({ data: { data: {} } });
  });

  it('sends the typed text without the spaces around it', async () => {
    const { container } = render(<InstancesView accountId='acc-1' heading='Instances' serviceName='AmazonEC2' />);
    await waitFor(() => expect(listQueries()).toHaveLength(1));

    fireEvent.change(searchBox(container), { target: { value: '  nudgebee  ' } });
    fireEvent.keyDown(searchBox(container), { key: 'Enter' });

    await waitFor(() => expect(listQueries()).toHaveLength(2));
    expect(listQueries()[1]).toContain('"%nudgebee%"');
    expect(listQueries()[1]).not.toContain('nudgebee ');
    expect(listQueries()[1]).not.toContain(' nudgebee');
  });
});
