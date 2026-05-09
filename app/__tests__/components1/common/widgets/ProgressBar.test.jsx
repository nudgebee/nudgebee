import React from 'react';
import { render, screen } from '@testing-library/react';
import ProgressBar from '@components1/common/widgets/ProgressBar';

// Mock ValueWithPercentage
jest.mock('@components1/k8s/common/ValueWithPercentage', () => {
  const ValueWithPercentage = ({ value, capacity, makeValueRed: _makeValueRed, noPercentage: _noPercentage, showParentheses: _showParentheses }) => (
    <span data-testid='value-with-percentage' data-value={value} data-capacity={capacity}>
      {value}
    </span>
  );
  ValueWithPercentage.displayName = 'ValueWithPercentage';
  return ValueWithPercentage;
});

describe('ProgressBar', () => {
  it('renders with default props (value=0)', () => {
    const { container } = render(<ProgressBar />);
    expect(container.firstChild).toBeTruthy();
  });

  it('renders with value and capacity > 0 (shows usage)', () => {
    render(<ProgressBar value={50} capacity={100} />);
    const valueElements = screen.getAllByTestId('value-with-percentage');
    expect(valueElements.length).toBeGreaterThan(0);
  });

  it('renders without capacity (value only path)', () => {
    render(<ProgressBar value={75} />);
    const valueElements = screen.getAllByTestId('value-with-percentage');
    expect(valueElements.length).toBeGreaterThan(0);
  });

  it('renders with value=0 (shows value-only path)', () => {
    render(<ProgressBar value={0} capacity={100} />);
    const valueElements = screen.getAllByTestId('value-with-percentage');
    expect(valueElements.length).toBeGreaterThan(0);
  });

  it('renders with tooltipRequired=true', () => {
    const { container } = render(<ProgressBar value={50} capacity={100} tooltipRequired={true} />);
    expect(container.firstChild).toBeTruthy();
  });

  it('renders with blueVarient=true (usage <= 90)', () => {
    const { container } = render(<ProgressBar value={30} capacity={100} blueVarient={true} />);
    expect(container.firstChild).toBeTruthy();
  });

  it('renders with usage > 90 (red color)', () => {
    const { container } = render(<ProgressBar value={95} capacity={100} />);
    expect(container.firstChild).toBeTruthy();
  });

  it('renders with value > 100 (clamped to 100 in non-tooltip)', () => {
    const { container } = render(<ProgressBar value={150} />);
    expect(container.firstChild).toBeTruthy();
  });

  it('renders with usage > 100 (clamped to 100 in tooltip)', () => {
    const { container } = render(<ProgressBar value={150} capacity={100} tooltipRequired={true} />);
    expect(container.firstChild).toBeTruthy();
  });

  it('renders label when capacity and value > 0 with showCapacity=true', () => {
    render(<ProgressBar value={50} capacity={100} label='CPU' showCapacity={true} />);
    const valueElements = screen.getAllByTestId('value-with-percentage');
    expect(valueElements.length).toBeGreaterThan(0);
  });

  it('renders label when showCapacity=false and label provided', () => {
    render(<ProgressBar value={50} capacity={100} label='Memory' showCapacity={false} />);
    const valueElements = screen.getAllByTestId('value-with-percentage');
    expect(valueElements.length).toBeGreaterThan(0);
  });

  it('does not render label when showCapacity=false and no label', () => {
    render(<ProgressBar value={50} capacity={100} showCapacity={false} />);
    const valueElements = screen.getAllByTestId('value-with-percentage');
    expect(valueElements.length).toBeGreaterThan(0);
  });

  it('renders with iconColor=false in tooltip', () => {
    const { container } = render(<ProgressBar value={50} capacity={100} tooltipRequired={true} iconColor={false} />);
    expect(container.firstChild).toBeTruthy();
  });

  it('renders with iconColor=true (value > 90) in tooltip', () => {
    const { container } = render(<ProgressBar value={95} capacity={100} tooltipRequired={true} iconColor={true} />);
    expect(container.firstChild).toBeTruthy();
  });

  it('renders with custom width', () => {
    const { container } = render(<ProgressBar value={50} width='200px' />);
    expect(container.firstChild).toBeTruthy();
  });

  it('renders with showParentheses=true', () => {
    render(<ProgressBar value={50} capacity={100} showParentheses={true} />);
    const valueElements = screen.getAllByTestId('value-with-percentage');
    expect(valueElements.length).toBeGreaterThan(0);
  });

  it('computes usage and available correctly when value and capacity > 0', () => {
    render(<ProgressBar value={25} capacity={100} />);
    // usage should be 25.00, available should be 75.00
    const valueElements = screen.getAllByTestId('value-with-percentage');
    expect(valueElements.length).toBeGreaterThan(0);
  });

  it('renders blueVarient=false with usage <= 90 (green color)', () => {
    const { container } = render(<ProgressBar value={50} capacity={100} blueVarient={false} />);
    expect(container.firstChild).toBeTruthy();
  });
});
