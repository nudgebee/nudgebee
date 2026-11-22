import * as React from 'react';
import '@testing-library/jest-dom';
import { fireEvent, render, screen } from '@testing-library/react';
import { OverviewView } from '@components/llm/gateway-usage/views/OverviewView';

let chartProps: any;
jest.mock('@lib/auth', () => ({ __esModule: true, canViewGatewayUserAccess: () => true }));
jest.mock('@ui/Chart', () => ({
  __esModule: true,
  default: {
    TimeSeries: (props: any) => {
      chartProps = props;
      return null;
    },
  },
}));
jest.mock('@shared/tables/CustomTable', () => ({ __esModule: true, default: () => null }));

const filters = { granularity: 'day' } as React.ComponentProps<typeof OverviewView>['filters'];

describe('AI Gateway overview chart legend (#40915)', () => {
  it('shows the token total in short form with one decimal (15,302,648 → 15.3M)', () => {
    render(<OverviewView metrics={null} filters={filters} loading={false} error={null} />);
    fireEvent.click(screen.getByText('Tokens'));
    expect(chartProps.legendFormat(15_302_648)).toBe('15.3M');
    expect(chartProps.format(15_302_648)).toBe('15,302,648');
  });

  it('keeps the full cost total on the cost metric', () => {
    render(<OverviewView metrics={null} filters={filters} loading={false} error={null} />);
    expect(chartProps.legendFormat).toBeUndefined();
  });
});
