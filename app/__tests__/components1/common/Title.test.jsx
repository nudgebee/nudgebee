import React from 'react';
import { render, screen } from '@testing-library/react';
import Title from '@components1/common/Title';

jest.mock('src/utils/colors', () => ({
  colors: {
    text: {
      secondary: '#374151',
      primary: '#3B82F6',
      white: '#fff',
      tertiary: '#6B7280',
      title: '#111827',
      primaryLight: '#60A5FA',
      success: '#16a34a',
      disabledInput: '#9CA3AF',
      secondaryDark: '#1F2937',
    },
    background: {
      primaryLightest: '#EFF6FF',
      titleUnderline: '#3B82F6',
      white: '#fff',
      transparent: 'transparent',
      switchTrackDark: '#3B82F6',
      tertiaryLightest: '#F0F9FF',
    },
    border: { secondary: '#D1D5DB', primary: '#3B82F6', success: '#22C55E' },
    button: {
      primary: '#3B82F6',
      primaryText: '#fff',
      primaryHover: '#2563EB',
      primaryDisabled: '#93C5FD',
      primaryDisabledText: '#fff',
      secondary: '#fff',
      secondaryBorder: '#D1D5DB',
      secondaryText: '#374151',
      secondaryHover: '#F9FAFB',
      secondaryHoverBorder: '#9CA3AF',
      secondaryDisabled: '#F3F4F6',
      secondaryDisabledText: '#9CA3AF',
      secondaryDisabledBorder: '#E5E7EB',
      tertiary: '#EFF6FF',
      tertiaryBorder: '#BFDBFE',
      tertiaryText: '#3B82F6',
      tertiaryHover: '#DBEAFE',
      tertiaryDisabled: '#F9FAFB',
      tertiaryDisabledText: '#93C5FD',
      tertiaryDisabledBorder: '#DBEAFE',
    },
    primary: '#3B82F6',
  },
}));

describe('Title', () => {
  it('renders the title text', () => {
    render(<Title title='My Title' />);
    expect(screen.getByText('My Title')).toBeInTheDocument();
  });

  it('renders with a span underline by default (isUnderline=true)', () => {
    const { container } = render(<Title title='My Title' />);
    const span = container.querySelector('span');
    expect(span).toBeInTheDocument();
  });

  it('does not render underline span when isUnderline=false', () => {
    const { container } = render(<Title title='My Title' isUnderline={false} />);
    const span = container.querySelector('span');
    expect(span).not.toBeInTheDocument();
  });

  it('renders underline span with default (non-light) styles', () => {
    const { container } = render(<Title title='My Title' isUnderline={true} lightVariant={false} />);
    const span = container.querySelector('span');
    expect(span).toBeInTheDocument();
    expect(span).toHaveStyle({ width: '24px' });
    expect(span).toHaveStyle({ backgroundColor: '#3B82F6' });
  });

  it('renders underline span with light variant styles', () => {
    const { container } = render(<Title title='My Title' isUnderline={true} lightVariant={true} />);
    const span = container.querySelector('span');
    expect(span).toBeInTheDocument();
    expect(span).toHaveStyle({ width: '16px' });
    expect(span).toHaveStyle({ backgroundColor: '#60A5FA' });
    expect(span).toHaveStyle({ borderRadius: '2px' });
  });

  it('renders wrapper div with inline-flex display', () => {
    const { container } = render(<Title title='My Title' />);
    const wrapper = container.firstChild;
    expect(wrapper.tagName).toBe('DIV');
    expect(wrapper).toHaveStyle({ display: 'inline-flex' });
    expect(wrapper).toHaveStyle({ flexDirection: 'column' });
  });

  it('renders with lightVariant=false by default', () => {
    render(<Title title='Default Title' />);
    expect(screen.getByText('Default Title')).toBeInTheDocument();
  });

  it('renders with lightVariant=true', () => {
    render(<Title title='Light Title' lightVariant={true} />);
    expect(screen.getByText('Light Title')).toBeInTheDocument();
  });

  it('accepts and applies sx prop without crashing', () => {
    expect(() => render(<Title title='Styled Title' sx={{ color: 'red' }} />)).not.toThrow();
  });

  it('renders title as a string', () => {
    render(<Title title='String Title' />);
    expect(screen.getByText('String Title')).toBeInTheDocument();
  });

  it('renders when title is a number', () => {
    render(<Title title={42} />);
    expect(screen.getByText('42')).toBeInTheDocument();
  });

  it('renders without crashing when no props are provided', () => {
    expect(() => render(<Title />)).not.toThrow();
  });

  it('renders without underline when isUnderline=false and lightVariant=true', () => {
    const { container } = render(<Title title='No Underline Light' isUnderline={false} lightVariant={true} />);
    expect(screen.getByText('No Underline Light')).toBeInTheDocument();
    expect(container.querySelector('span')).not.toBeInTheDocument();
  });

  it('default underline span has height of 2', () => {
    const { container } = render(<Title title='My Title' />);
    const span = container.querySelector('span');
    expect(span).toHaveStyle({ height: '2px' });
  });
});
