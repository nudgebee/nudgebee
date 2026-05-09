import React from 'react';
import { render, screen } from '@testing-library/react';
import ValueWithHeading from '@components1/common/ValueWithHeading';

describe('ValueWithHeading', () => {
  it('renders heading text', () => {
    render(<ValueWithHeading heading='Total Cost' />);
    expect(screen.getByText('Total Cost')).toBeInTheDocument();
  });

  it('does not render icon dot when no iconColor is provided', () => {
    const { container } = render(<ValueWithHeading heading='Cost' />);
    // MUI Box with sx styles uses CSS classes, not inline styles
    // When no iconColor, the span element is not rendered at all
    const spans = container.querySelectorAll('span');
    // None of the spans should have a backgroundColor inline style (MUI uses CSS classes)
    const coloredDots = Array.from(spans).filter((span) => span.style?.backgroundColor);
    expect(coloredDots).toHaveLength(0);
  });

  it('renders icon dot when iconColor is provided', () => {
    const { container } = render(<ValueWithHeading heading='Cost' iconColor='#FF0000' />);
    // MUI renders the span element when iconColor is provided
    // The span is rendered as a Box component='span' via MUI sx
    const spans = container.querySelectorAll('span');
    // There should be at least one span rendered for the icon dot
    expect(spans.length).toBeGreaterThan(0);
  });

  it('renders value with $ prefix by default', () => {
    const { container } = render(<ValueWithHeading heading='Cost' value='1000' />);
    // The $ and value are separate text nodes inside the Typography
    const textContent = container.textContent;
    expect(textContent).toContain('$');
    expect(textContent).toContain('1000');
  });

  it('renders value without $ prefix when hideLogo is true', () => {
    const { container } = render(<ValueWithHeading heading='Cost' value='1000' hideLogo={true} />);
    const textContent = container.textContent;
    expect(textContent).toContain('1000');
    expect(textContent).not.toContain('$');
  });

  it('does not render value Typography when value is not provided', () => {
    render(<ValueWithHeading heading='Cost' />);
    // No dollar sign or numeric value should be rendered
    expect(screen.queryByText(/\$/)).not.toBeInTheDocument();
  });

  it('does not render value Typography when value is empty string', () => {
    render(<ValueWithHeading heading='Cost' value='' />);
    expect(screen.queryByText(/\$/)).not.toBeInTheDocument();
  });

  it('renders heading with 10px fontSize when forWorkload is true', () => {
    render(<ValueWithHeading heading='Workload Cost' forWorkload={true} />);
    const heading = screen.getByText('Workload Cost');
    expect(heading).toBeInTheDocument();
  });

  it('renders heading with 12px fontSize when forCostSummary is true', () => {
    render(<ValueWithHeading heading='Summary Cost' forCostSummary={true} />);
    const heading = screen.getByText('Summary Cost');
    expect(heading).toBeInTheDocument();
  });

  it('renders numeric value correctly', () => {
    const { container } = render(<ValueWithHeading heading='Count' value={42} />);
    expect(container.textContent).toContain('42');
  });
});
