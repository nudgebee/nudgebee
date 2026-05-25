import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import CustomAccordion from '@components1/common/CustomAccordion';

jest.mock('@components1/runbooks/styles', () => ({
  styles: { accordion: {} },
}));

describe('CustomAccordion', () => {
  test('renders with title', () => {
    render(<CustomAccordion title='My Title' />);
    expect(screen.getByText('My Title')).toBeInTheDocument();
  });

  test('renders description when provided', () => {
    render(<CustomAccordion title='Title' description='Some description here' />);
    expect(screen.getByText('Some description here')).toBeInTheDocument();
  });

  test('does not render description when not provided', () => {
    render(<CustomAccordion title='Title' />);
    // No description should be visible
    expect(screen.queryByText('Some description here')).not.toBeInTheDocument();
  });

  test('shows expand icon when children provided', () => {
    const { container } = render(
      <CustomAccordion title='With Children'>
        <div>Child content</div>
      </CustomAccordion>
    );
    // MUI ExpandMoreIcon renders as an svg when children are present
    // The icon is rendered via MUI internally; verify by checking SVG presence
    expect(container.querySelector('svg')).toBeInTheDocument();
  });

  test('no expand icon when no children', () => {
    const { container } = render(<CustomAccordion title='No Children' />);
    // When expandIcon is null, no expand icon svg is rendered in the summary
    // MUI AccordionSummary renders no expand button when expandIcon is null
    const expandButton = container.querySelector('.MuiAccordionSummary-expandIconWrapper');
    // The wrapper may exist but be empty
    if (expandButton) {
      expect(expandButton.children.length).toBe(0);
    }
  });

  test('calls onChange when accordion clicked', () => {
    const onChange = jest.fn();
    render(
      <CustomAccordion title='Clickable' onChange={onChange}>
        <div>Content</div>
      </CustomAccordion>
    );
    const summary = screen.getByRole('button');
    fireEvent.click(summary);
    expect(onChange).toHaveBeenCalledTimes(1);
  });

  test('renders icon when provided', () => {
    render(<CustomAccordion title='With Icon' icon={<span data-testid='custom-icon'>Icon</span>} />);
    expect(screen.getByTestId('custom-icon')).toBeInTheDocument();
  });

  test('accordion has correct id based on title (replacing spaces with dashes)', () => {
    const { container } = render(<CustomAccordion title='My Cool Title' />);
    const accordion = container.querySelector('#panel-header-My-Cool-Title');
    expect(accordion).toBeInTheDocument();
  });

  test('renders children in AccordionDetails', () => {
    render(
      <CustomAccordion title='Test' expanded={true}>
        <div data-testid='accordion-child'>Child content here</div>
      </CustomAccordion>
    );
    expect(screen.getByTestId('accordion-child')).toBeInTheDocument();
  });
});
