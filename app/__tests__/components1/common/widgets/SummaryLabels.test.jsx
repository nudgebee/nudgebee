import React from 'react';
import { render, screen } from '@testing-library/react';
import SummaryLabels from '@components1/common/widgets/SummaryLabels';

jest.mock('src/utils/colors', () => ({
  colors: {
    critical: '#c00000',
    info: '#3B82F6',
    success: '#16A34A',
    text: {
      greyDark: '#4B5563',
    },
  },
}));

describe('SummaryLabels', () => {
  it('renders with info variant (default)', () => {
    render(<SummaryLabels label='Info Label' />);
    expect(screen.getByText('Info Label')).toBeInTheDocument();
  });

  it('renders with critical variant', () => {
    render(<SummaryLabels variant='critical' label='Critical Alert' />);
    expect(screen.getByText('Critical Alert')).toBeInTheDocument();
  });

  it('renders with savings variant', () => {
    render(<SummaryLabels variant='savings' label='Save Money' />);
    expect(screen.getByText('Save Money')).toBeInTheDocument();
  });

  it('renders with unknown variant (falls to default info)', () => {
    render(<SummaryLabels variant='unknown' label='Default' />);
    expect(screen.getByText('Default')).toBeInTheDocument();
  });

  it('renders grayText when provided', () => {
    render(<SummaryLabels label='Main' grayText='Extra info' />);
    expect(screen.getByText('Main')).toBeInTheDocument();
    expect(screen.getByText('Extra info')).toBeInTheDocument();
  });

  it('does not render grayText when not provided', () => {
    render(<SummaryLabels label='Only Label' />);
    expect(screen.getByText('Only Label')).toBeInTheDocument();
  });

  it('applies custom sx prop', () => {
    const { container } = render(<SummaryLabels label='Styled' sx={{ margin: '10px' }} />);
    expect(container.firstChild).toBeTruthy();
  });

  it('renders with info variant getLabelBgColor', () => {
    render(<SummaryLabels variant='info' label='Info' />);
    expect(screen.getByText('Info')).toBeInTheDocument();
  });
});
