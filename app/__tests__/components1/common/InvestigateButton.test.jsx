import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import InvestigateButton from '@components1/common/InvestigateButton';

jest.mock('src/utils/colors', () => ({
  colors: {
    text: { secondary: '#374151', primary: '#3B82F6', white: '#fff', tertiary: '#6B7280' },
    background: { primaryLightest: '#EFF6FF', white: '#fff', transparent: 'transparent' },
    border: { secondary: '#D1D5DB', primary: '#3B82F6' },
    primary: '#3B82F6',
    button: {
      primary: '#3B82F6',
      primaryText: '#fff',
      tertiaryBorder: '#BFDBFE',
      secondary: '#fff',
      secondaryBorder: '#D1D5DB',
      secondaryText: '#374151',
    },
  },
}));

jest.mock('next/router', () => ({ useRouter: jest.fn(() => ({ push: jest.fn(), pathname: '/', asPath: '/' })) }));

jest.mock('next/link', () => ({
  __esModule: true,
  default: ({ href, children, ...rest }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));

jest.mock('react-icons/fi', () => ({ FiArrowRight: () => <svg data-testid='arrow-icon' /> }));

jest.mock('@components1/common/CustomTooltip', () => ({
  __esModule: true,
  default: ({ children }) => <>{children}</>,
}));

jest.mock('src/utils/actionStyles', () => ({ action: { investigateOutline: {} } }));

describe('InvestigateButton', () => {
  it('renders button with aria-label Troubleshoot', () => {
    render(<InvestigateButton />);
    expect(screen.getByRole('button', { name: 'Troubleshoot' })).toBeInTheDocument();
  });

  it('when displayText=true: renders default text "Investigate"', () => {
    render(<InvestigateButton displayText={true} />);
    expect(screen.getByText('Investigate')).toBeInTheDocument();
  });

  it('when displayText=true: renders custom text prop', () => {
    render(<InvestigateButton displayText={true} text='Analyze' />);
    expect(screen.getByText('Analyze')).toBeInTheDocument();
  });

  it('when displayText=false: renders just the icon button (no text span)', () => {
    render(<InvestigateButton displayText={false} />);
    expect(screen.queryByText('Investigate')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Troubleshoot' })).toBeInTheDocument();
  });

  it('calls onClick and stopPropagation when clicked', () => {
    const onClick = jest.fn();
    render(<InvestigateButton onClick={onClick} />);
    const button = screen.getByRole('button', { name: 'Troubleshoot' });
    fireEvent.click(button);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('renders as Link (a tag) when url is provided', () => {
    render(<InvestigateButton url='/some/path' />);
    const link = screen.getByRole('link', { name: 'Troubleshoot' });
    expect(link).toBeInTheDocument();
    expect(link).toHaveAttribute('href', '/some/path');
  });

  it('renders as button when no url', () => {
    render(<InvestigateButton />);
    const button = screen.getByRole('button', { name: 'Troubleshoot' });
    expect(button).toBeInTheDocument();
    expect(button.tagName).toBe('BUTTON');
  });
});
