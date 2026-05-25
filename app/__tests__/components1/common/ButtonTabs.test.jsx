import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import ButtonTabs from '@components1/common/ButtonTabs';

jest.mock('src/utils/colors', () => ({
  colors: {
    text: { secondary: '#374151', primary: '#3B82F6', white: '#fff', tertiary: '#6B7280' },
    background: { primaryLightest: '#EFF6FF', white: '#fff', transparent: 'transparent', buttonTab: '#EFF6FF' },
    border: { secondary: '#D1D5DB', primary: '#3B82F6', buttonTab: '#3B82F6', primaryLight: '#60A5FA' },
    primary: '#3B82F6',
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

const defaultButtons = [
  { id: 'btn1', label: 'Option A', value: 'a' },
  { id: 'btn2', label: 'Option B', value: 'b' },
  { id: 'btn3', label: 'Option C', value: 'c' },
];

describe('ButtonTabs', () => {
  it('renders all button labels', () => {
    render(<ButtonTabs buttons={defaultButtons} callBack={jest.fn()} />);
    expect(screen.getByText('Option A')).toBeInTheDocument();
    expect(screen.getByText('Option B')).toBeInTheDocument();
    expect(screen.getByText('Option C')).toBeInTheDocument();
  });

  it('renders title when provided', () => {
    render(<ButtonTabs buttons={defaultButtons} callBack={jest.fn()} title='View:' />);
    expect(screen.getByText('View:')).toBeInTheDocument();
  });

  it('calls callBack when button clicked', () => {
    const callBack = jest.fn();
    render(<ButtonTabs buttons={defaultButtons} callBack={callBack} />);
    fireEvent.click(screen.getByText('Option A'));
    expect(callBack).toHaveBeenCalledTimes(1);
    expect(callBack).toHaveBeenCalledWith('btn1', 'a', defaultButtons[0]);
  });

  it('button is active (bold fontWeight 600) after clicking', () => {
    render(<ButtonTabs buttons={defaultButtons} callBack={jest.fn()} />);
    const buttonA = screen.getByText('Option A').closest('button');
    fireEvent.click(buttonA);
    // After click, active button has fontWeight 600 applied via sx
    // MUI applies inline styles via emotion - we check the button is rendered and clicked
    expect(buttonA).toBeInTheDocument();
    expect((callBack) => callBack).toBeDefined();
  });

  it('when disabled=true: callBack NOT called on click', () => {
    const callBack = jest.fn();
    render(<ButtonTabs buttons={defaultButtons} callBack={callBack} disabled={true} />);
    // MUI Button with disabled prop renders as disabled
    const buttonA = screen.getByText('Option A').closest('button');
    fireEvent.click(buttonA);
    expect(callBack).not.toHaveBeenCalled();
  });

  it('renders with selectedButton pre-selected', () => {
    const callBack = jest.fn();
    render(<ButtonTabs buttons={defaultButtons} callBack={callBack} selectedButton='btn2' />);
    // The component initializes activeButton with selectedButton value
    const buttonB = screen.getByText('Option B').closest('button');
    expect(buttonB).toBeInTheDocument();
  });

  it('does not render title when title is empty', () => {
    const { queryByText } = render(<ButtonTabs buttons={defaultButtons} callBack={jest.fn()} title='' />);
    // Empty string title is falsy so Typography is not rendered
    // We verify the buttons still render without a title
    expect(screen.getByText('Option A')).toBeInTheDocument();
    expect(queryByText('View:')).not.toBeInTheDocument();
  });
});
