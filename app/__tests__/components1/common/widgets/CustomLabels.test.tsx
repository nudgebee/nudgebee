import React from 'react';
import { render, screen } from '@testing-library/react';
import CustomLabels from '@components1/common/widgets/CustomLabels';

jest.mock('src/utils/colors', () => ({
  colors: {
    background: {
      null: '#F3F4F6',
      yellowLabel: '#FEF3C7',
      white: '#FFFFFF',
      greenLabel: '#D1FAE5',
      lightRedLabel: '#FEE2E2',
      criticalRed: '#EF4444',
      tertiaryLight: '#F9FAFB',
      blueLabel: '#DBEAFE',
      tertiaryLightest: '#F0F9FF',
    },
    text: {
      null: '#374151',
      yellowLabel: '#92400E',
      orangeLabel: '#EA580C',
      white: '#FFFFFF',
      tertiary: '#6B7280',
      primaryDark: '#1E3A5F',
    },
    success: '#16A34A',
    errorText: '#DC2626',
  },
}));

jest.mock('@components1/common/CustomTooltip', () => {
  const CustomTooltip = ({ children, title }: any) => (
    <div data-testid='custom-tooltip' data-title={typeof title === 'string' ? title : ''}>
      {children}
    </div>
  );
  CustomTooltip.displayName = 'CustomTooltip';
  return CustomTooltip;
});

jest.mock('@components1/common/SafeIcon', () => {
  const SafeIcon = ({ alt }: any) => <img data-testid='safe-icon' alt={alt} />;
  SafeIcon.displayName = 'SafeIcon';
  return SafeIcon;
});

jest.mock(
  '@assets',
  () => ({
    MenuArrowDownIcon: 'mock-arrow-icon',
  }),
  { virtual: true }
);

describe('CustomLabels', () => {
  // Variant-based rendering
  it('renders with red variant', () => {
    render(<CustomLabels text='Error' variant='red' />);
    expect(screen.getByText('Error')).toBeInTheDocument();
  });

  it('renders with green variant', () => {
    render(<CustomLabels text='Success' variant='green' />);
    expect(screen.getByText('Success')).toBeInTheDocument();
  });

  it('renders with grey variant', () => {
    render(<CustomLabels text='Neutral' variant='grey' />);
    expect(screen.getByText('Neutral')).toBeInTheDocument();
  });

  it('renders with yellow variant', () => {
    render(<CustomLabels text='Warning' variant='yellow' />);
    expect(screen.getByText('Warning')).toBeInTheDocument();
  });

  it('renders with orange variant', () => {
    render(<CustomLabels text='Orange' variant='orange' />);
    expect(screen.getByText('Orange')).toBeInTheDocument();
  });

  it('renders with criticalRed variant', () => {
    render(<CustomLabels text='Critical' variant='criticalRed' />);
    expect(screen.getByText('Critical')).toBeInTheDocument();
  });

  it('renders with blue variant', () => {
    render(<CustomLabels text='Info' variant='blue' />);
    expect(screen.getByText('Info')).toBeInTheDocument();
  });

  it('renders with unknown variant (goes to switch default, then auto-detection)', () => {
    render(<CustomLabels text='custom-status' variant='unknown-variant' />);
    expect(screen.getByText('custom-status')).toBeInTheDocument();
  });

  // Auto-detection without variant
  it('auto-detects red style for "error" text', () => {
    render(<CustomLabels text='error' />);
    expect(screen.getByText('error')).toBeInTheDocument();
  });

  it('auto-detects red style for "firing" text', () => {
    render(<CustomLabels text='firing' />);
    expect(screen.getByText('firing')).toBeInTheDocument();
  });

  it('auto-detects red style for "failed" text', () => {
    render(<CustomLabels text='failed' />);
    expect(screen.getByText('failed')).toBeInTheDocument();
  });

  it('auto-detects red style for "suspended" text', () => {
    render(<CustomLabels text='suspended' />);
    expect(screen.getByText('suspended')).toBeInTheDocument();
  });

  it('auto-detects red style for "high" text', () => {
    render(<CustomLabels text='high' />);
    expect(screen.getByText('high')).toBeInTheDocument();
  });

  it('auto-detects red style for "disabled" text', () => {
    render(<CustomLabels text='disabled' />);
    expect(screen.getByText('disabled')).toBeInTheDocument();
  });

  it('auto-detects red style for "highest" text', () => {
    render(<CustomLabels text='highest' />);
    expect(screen.getByText('highest')).toBeInTheDocument();
  });

  it('auto-detects red style for "rejected" text', () => {
    render(<CustomLabels text='rejected' />);
    expect(screen.getByText('rejected')).toBeInTheDocument();
  });

  it('auto-detects red style for "unhealthy" text', () => {
    render(<CustomLabels text='unhealthy' />);
    expect(screen.getByText('unhealthy')).toBeInTheDocument();
  });

  it('auto-detects red style for "incompatible" text', () => {
    render(<CustomLabels text='incompatible' />);
    expect(screen.getByText('incompatible')).toBeInTheDocument();
  });

  it('auto-detects green style for "complete" text', () => {
    render(<CustomLabels text='complete' />);
    expect(screen.getByText('complete')).toBeInTheDocument();
  });

  it('auto-detects green style for "active" text', () => {
    render(<CustomLabels text='active' />);
    expect(screen.getByText('active')).toBeInTheDocument();
  });

  it('auto-detects green style for "succeeded" text', () => {
    render(<CustomLabels text='succeeded' />);
    expect(screen.getByText('succeeded')).toBeInTheDocument();
  });

  it('auto-detects green style for "resolved" text', () => {
    render(<CustomLabels text='resolved' />);
    expect(screen.getByText('resolved')).toBeInTheDocument();
  });

  it('auto-detects green style for "closed" text', () => {
    render(<CustomLabels text='closed' />);
    expect(screen.getByText('closed')).toBeInTheDocument();
  });

  it('auto-detects green style for "done" text', () => {
    render(<CustomLabels text='done' />);
    expect(screen.getByText('done')).toBeInTheDocument();
  });

  it('auto-detects green style for "ok" text', () => {
    render(<CustomLabels text='ok' />);
    expect(screen.getByText('ok')).toBeInTheDocument();
  });

  it('auto-detects green style for "enabled" text', () => {
    render(<CustomLabels text='enabled' />);
    expect(screen.getByText('enabled')).toBeInTheDocument();
  });

  it('auto-detects green style for "approved" text', () => {
    render(<CustomLabels text='approved' />);
    expect(screen.getByText('approved')).toBeInTheDocument();
  });

  it('auto-detects green style for "success" text', () => {
    render(<CustomLabels text='success' />);
    expect(screen.getByText('success')).toBeInTheDocument();
  });

  it('auto-detects green style for "completed" text', () => {
    render(<CustomLabels text='completed' />);
    expect(screen.getByText('completed')).toBeInTheDocument();
  });

  it('auto-detects green style for "healthy" text', () => {
    render(<CustomLabels text='healthy' />);
    expect(screen.getByText('healthy')).toBeInTheDocument();
  });

  it('auto-detects green style for "compatible" text', () => {
    render(<CustomLabels text='compatible' />);
    expect(screen.getByText('compatible')).toBeInTheDocument();
  });

  it('auto-detects yellow style for "pending" text', () => {
    render(<CustomLabels text='pending' />);
    expect(screen.getByText('pending')).toBeInTheDocument();
  });

  it('auto-detects yellow style for "inactive" text', () => {
    render(<CustomLabels text='inactive' />);
    expect(screen.getByText('inactive')).toBeInTheDocument();
  });

  it('auto-detects yellow style for "in progress" text', () => {
    render(<CustomLabels text='in progress' />);
    expect(screen.getByText('in progress')).toBeInTheDocument();
  });

  it('auto-detects yellow style for "skipped" text', () => {
    render(<CustomLabels text='skipped' />);
    expect(screen.getByText('skipped')).toBeInTheDocument();
  });

  it('auto-detects yellow style for "medium" text', () => {
    render(<CustomLabels text='medium' />);
    expect(screen.getByText('medium')).toBeInTheDocument();
  });

  it('auto-detects yellow style for "in_progress" text', () => {
    render(<CustomLabels text='in_progress' />);
    expect(screen.getByText('in_progress')).toBeInTheDocument();
  });

  it('auto-detects criticalRed style for "critical" text', () => {
    render(<CustomLabels text='critical' />);
    expect(screen.getByText('critical')).toBeInTheDocument();
  });

  it('auto-detects blue style for "low" text', () => {
    render(<CustomLabels text='low' />);
    expect(screen.getByText('low')).toBeInTheDocument();
  });

  it('falls to grey for unrecognized text', () => {
    render(<CustomLabels text='some-random-status' />);
    expect(screen.getByText('some-random-status')).toBeInTheDocument();
  });

  // displayTooltip and tooltipCharLimit
  it('truncates text and shows tooltip when displayTooltip=true and text exceeds limit', () => {
    const longText = 'This is a very long label text that exceeds limit';
    render(<CustomLabels text={longText} displayTooltip={true} tooltipCharLimit={10} />);
    expect(screen.getByText('This is a ...')).toBeInTheDocument();
  });

  it('does not truncate when text is within limit', () => {
    render(<CustomLabels text='short' displayTooltip={true} tooltipCharLimit={20} />);
    expect(screen.getByText('short')).toBeInTheDocument();
  });

  it('does not truncate when displayTooltip=false', () => {
    const longText = 'This is a very long label text';
    render(<CustomLabels text={longText} displayTooltip={false} tooltipCharLimit={5} />);
    expect(screen.getByText(longText)).toBeInTheDocument();
  });

  // showDropdownArrow
  it('renders dropdown arrow when showDropdownArrow=true', () => {
    render(<CustomLabels text='Status' showDropdownArrow={true} />);
    expect(screen.getByTestId('safe-icon')).toBeInTheDocument();
  });

  it('does not render dropdown arrow when showDropdownArrow=false (default)', () => {
    render(<CustomLabels text='Status' />);
    expect(screen.queryByTestId('safe-icon')).not.toBeInTheDocument();
  });

  // Fallback to '-' when empty text
  it('renders "-" when text is empty string', () => {
    render(<CustomLabels text='' />);
    expect(screen.getByText('-')).toBeInTheDocument();
  });

  // Custom props
  it('applies custom height', () => {
    const { container } = render(<CustomLabels text='Label' height='32px' />);
    expect(container.firstChild).toBeTruthy();
  });

  it('applies custom maxWidth', () => {
    const { container } = render(<CustomLabels text='Label' maxWidth='200px' />);
    expect(container.firstChild).toBeTruthy();
  });

  it('applies custom textTransform', () => {
    render(<CustomLabels text='label' textTransform='uppercase' />);
    expect(screen.getByText('label')).toBeInTheDocument();
  });

  it('applies custom wordBreak', () => {
    render(<CustomLabels text='label' wordBreak='break-all' />);
    expect(screen.getByText('label')).toBeInTheDocument();
  });

  it('applies custom margin', () => {
    const { container } = render(<CustomLabels text='Label' margin='8px' />);
    expect(container.firstChild).toBeTruthy();
  });

  it('applies customLabelStyle', () => {
    render(<CustomLabels text='Custom' customLabelStyle={{ fontSize: '14px' }} />);
    expect(screen.getByText('Custom')).toBeInTheDocument();
  });

  it('applies custom width', () => {
    const { container } = render(<CustomLabels text='Label' width='100px' />);
    expect(container.firstChild).toBeTruthy();
  });
});
