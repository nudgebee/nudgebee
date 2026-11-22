import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Gauge, VmMetricsCell, gaugeTone } from '@components/vm/VmMetricsGauges';
import { ds } from '@utils/colors';

describe('gaugeTone', () => {
  it('is calm below 70, busy from 70, critical from 90', () => {
    expect(gaugeTone(0)).toBe(ds.green[500]);
    expect(gaugeTone(69.9)).toBe(ds.green[500]);
    expect(gaugeTone(70)).toBe(ds.amber[500]);
    expect(gaugeTone(89.9)).toBe(ds.amber[500]);
    expect(gaugeTone(90)).toBe(ds.red[500]);
    expect(gaugeTone(100)).toBe(ds.red[500]);
  });
});

describe('Gauge', () => {
  it('shows the rounded percentage', () => {
    render(<Gauge label='CPU' value={42.4} />);
    expect(screen.getByRole('img', { name: 'CPU 42%' })).toBeInTheDocument();
  });

  it('shows a dash, not 0%, when the figure is unknown', () => {
    render(<Gauge label='RAM' />);
    expect(screen.getByRole('img', { name: 'RAM unknown' })).toBeInTheDocument();
    expect(screen.queryByText('0%')).not.toBeInTheDocument();
  });

  it('clamps out-of-range values', () => {
    render(<Gauge label='CPU' value={250} />);
    expect(screen.getByRole('img', { name: 'CPU 100%' })).toBeInTheDocument();
  });
});

describe('VmMetricsCell', () => {
  it('shows both gauges and opens the history on click', async () => {
    const onOpen = jest.fn();
    render(<VmMetricsCell vmName='web-1' metric={{ cpuPct: 30, memPct: 80 }} onOpen={onOpen} />);
    expect(screen.getByRole('img', { name: 'CPU 30%' })).toBeInTheDocument();
    expect(screen.getByRole('img', { name: 'RAM 80%' })).toBeInTheDocument();

    await userEvent.setup().click(screen.getByRole('button', { name: 'Show CPU and memory history for web-1' }));
    expect(onOpen).toHaveBeenCalledTimes(1);
  });

  it('is still clickable with no metric, so the history can explain why', async () => {
    const onOpen = jest.fn();
    render(<VmMetricsCell vmName='web-1' onOpen={onOpen} />);
    expect(screen.queryByRole('img')).not.toBeInTheDocument();

    await userEvent.setup().click(screen.getByRole('button', { name: 'Show metrics for web-1' }));
    expect(onOpen).toHaveBeenCalledTimes(1);
  });

  it('shows a loading mark while the first fetch is in flight', () => {
    render(<VmMetricsCell vmName='web-1' loading onOpen={jest.fn()} />);
    expect(screen.getByRole('button', { name: 'Show metrics for web-1' })).toHaveTextContent('…');
  });
});
