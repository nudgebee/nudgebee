import React from 'react';
import { render, screen } from '@testing-library/react';

// Mock child components
jest.mock('@components1/common/Loader', () => () => <div data-testid='loader' />);
jest.mock('@components1/common/CustomTooltip', () => {
  const PropTypes = require('prop-types');
  function CustomTooltip({ children }) {
    return <div data-testid='custom-tooltip'>{children}</div>;
  }
  CustomTooltip.propTypes = { children: PropTypes.node };
  return CustomTooltip;
});

// Mock dayjs to have predictable outputs
jest.mock('dayjs', () => {
  const actual = jest.requireActual('dayjs');
  const mockDayjs = (...args) => {
    const instance = actual(...args);
    return {
      ...instance,
      subtract: (amount, unit) => mockDayjs(instance.subtract(amount, unit).toDate()),
      format: (fmt) => {
        if (fmt === 'D MMM (ddd)') return 'testDay';
        if (fmt === 'h:mm A') return '12:00 PM';
        return instance.format(fmt);
      },
    };
  };
  Object.assign(mockDayjs, actual);
  return mockDayjs;
});

import CustomHeatMap from '@components1/common/charts/CustomHeatMap';

describe('CustomHeatMap', () => {
  const xAxisLabels = ['Day1', 'Day2'];
  const yAxisLabels = ['00:00', '01:00', '02:00', '03:00', '04:00'];

  it('renders loader when loading=true', () => {
    render(<CustomHeatMap loading={true} xAxisLabels={xAxisLabels} yAxisLabels={yAxisLabels} />);
    expect(screen.getByTestId('loader')).toBeInTheDocument();
  });

  it('renders heatmap cells when loading=false', () => {
    render(<CustomHeatMap loading={false} data={[]} xAxisLabels={xAxisLabels} yAxisLabels={yAxisLabels} />);
    // Should render day labels
    expect(screen.getAllByText('Day1').length).toBeGreaterThan(0);
  });

  it('renders with showTooltip=true (default) showing tooltip title', () => {
    const data = [{ day: 'Day1', hour: '00:00', value: 2, cpu: '50', memory: '100', rps: '10' }];
    render(<CustomHeatMap loading={false} data={data} xAxisLabels={['Day1']} yAxisLabels={['00:00']} showTooltip={true} selectedOption={0} />);
    expect(screen.getByTestId('custom-tooltip')).toBeInTheDocument();
  });

  it('renders with showTooltip=false (tooltip title undefined)', () => {
    const data = [{ day: 'Day1', hour: '00:00', value: 2, cpu: '50', memory: '100', rps: '10' }];
    render(<CustomHeatMap loading={false} data={data} xAxisLabels={['Day1']} yAxisLabels={['00:00']} showTooltip={false} selectedOption={1} />);
    expect(screen.getByTestId('custom-tooltip')).toBeInTheDocument();
  });

  it('renders selectedOption=2 (rps data)', () => {
    const data = [{ day: 'Day1', hour: '00:00', value: 3, cpu: '50', memory: '100', rps: '20' }];
    render(<CustomHeatMap loading={false} data={data} xAxisLabels={['Day1']} yAxisLabels={['00:00']} showTooltip={true} selectedOption={2} />);
    expect(screen.getByTestId('custom-tooltip')).toBeInTheDocument();
  });

  it('renders y-axis labels and skips non-multiple-of-4 ones', () => {
    const manyLabels = ['00:00', '01:00', '02:00', '03:00', '04:00', '05:00', '06:00', '07:00'];
    render(<CustomHeatMap loading={false} data={[]} xAxisLabels={['Day1']} yAxisLabels={manyLabels} />);
    // index 0 and 4 should render their text, others render null
    expect(screen.getByText('00:00')).toBeInTheDocument();
    expect(screen.getByText('04:00')).toBeInTheDocument();
  });

  it('renders with custom colors', () => {
    render(
      <CustomHeatMap
        loading={false}
        data={[]}
        xAxisLabels={['Day1']}
        yAxisLabels={['00:00']}
        customColors={['#FFF', '#EEE', '#DDD', '#CCC', '#BBB', '#AAA']}
      />
    );
    expect(screen.queryByTestId('loader')).not.toBeInTheDocument();
  });

  it('uses default props (no props passed except loading=false)', () => {
    // Uses default xAxisLabels (7 days) and default yAxisLabels (24 hours)
    render(<CustomHeatMap loading={false} data={[]} />);
    expect(screen.queryByTestId('loader')).not.toBeInTheDocument();
  });

  it('handles data with hour not matching yAxisLabel (default value = 0)', () => {
    const data = [{ day: 'Day1', hour: '99:99', value: 1, cpu: '10', memory: '20', rps: '5' }];
    render(<CustomHeatMap loading={false} data={data} xAxisLabels={['Day1']} yAxisLabels={['00:00']} selectedOption={0} />);
    // Even though hour doesn't match, should render with default value=0
    expect(screen.getByText('0')).toBeInTheDocument();
  });

  it('renders with orientation prop', () => {
    const { container } = render(<CustomHeatMap loading={false} data={[]} xAxisLabels={['Day1']} yAxisLabels={['00:00']} orientation='vertical' />);
    expect(container.querySelector('.heatmap.vertical')).toBeInTheDocument();
  });
});
