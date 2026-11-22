import * as React from 'react';
import '@testing-library/jest-dom';
import { render, screen } from '@testing-library/react';
import { canViewGatewayUserAccess } from '@lib/auth';
import { listGatewayModels } from '@api1/gateway-config';
import { OverviewView } from '@components/llm/gateway-usage/views/OverviewView';
import { GatewayRestrictionPicker } from '@components/common/settings/GatewayRestrictions';

jest.mock('@lib/auth', () => ({ __esModule: true, canViewGatewayUserAccess: jest.fn() }));
jest.mock('@api1/gateway-config', () => ({
  __esModule: true,
  GATEWAY_MATCH_PROVIDER_OPTIONS: [{ value: 'anthropic', label: 'anthropic' }],
  listGatewayModels: jest.fn().mockResolvedValue([]),
}));
// Charts and tables are not under test.
jest.mock('@ui/Chart', () => ({ __esModule: true, default: { TimeSeries: () => null } }));
jest.mock('@shared/tables/CustomTable', () => ({ __esModule: true, default: () => null }));

const asViewer = (viewer: boolean) => (canViewGatewayUserAccess as jest.Mock).mockReturnValue(viewer);
const filters = { granularity: 'day' } as React.ComponentProps<typeof OverviewView>['filters'];

describe('AI Gateway views for non-admins (#38965)', () => {
  beforeEach(() => jest.clearAllMocks());

  it('labels the overview as the caller’s own usage and drops the per-user card', () => {
    asViewer(false);
    render(<OverviewView metrics={null} filters={filters} loading={false} error={null} />);
    expect(screen.getByText('Your usage')).toBeInTheDocument();
    expect(screen.queryByText('By user')).not.toBeInTheDocument();
  });

  it('keeps the tenant-wide overview for tenant admins', () => {
    asViewer(true);
    render(<OverviewView metrics={null} filters={filters} loading={false} error={null} />);
    expect(screen.queryByText('Your usage')).not.toBeInTheDocument();
    expect(screen.getByText('By user')).toBeInTheDocument();
  });

  it('skips the admin-only model catalog in the key restriction picker', () => {
    asViewer(false);
    render(<GatewayRestrictionPicker value={{ providers: [], models: [], endpoints: [] }} onChange={() => {}} />);
    expect(listGatewayModels).not.toHaveBeenCalled();
  });

  it('loads the model catalog for tenant admins', () => {
    asViewer(true);
    render(<GatewayRestrictionPicker value={{ providers: [], models: [], endpoints: [] }} onChange={() => {}} />);
    expect(listGatewayModels).toHaveBeenCalledTimes(1);
  });
});
