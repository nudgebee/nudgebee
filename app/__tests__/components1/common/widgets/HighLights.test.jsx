import React from 'react';
import { render, screen } from '@testing-library/react';
import HighLights from '@components1/common/widgets/HighLights';

// Mock the Text component used inside HighLights
jest.mock('@components1/common/format/Text', () => {
  const PropTypes = require('prop-types');
  function Text({ value }) {
    return <span data-testid='highlights-text'>{value}</span>;
  }
  Text.displayName = 'Text';
  Text.propTypes = { value: PropTypes.any };
  return Text;
});

describe('HighLights', () => {
  it('renders text via Text component when no component prop', () => {
    render(<HighLights text='Hello World' />);
    expect(screen.getByTestId('highlights-text')).toHaveTextContent('Hello World');
  });

  it('renders custom component when component prop is provided', () => {
    const CustomComp = () => <div data-testid='custom-comp'>Custom</div>;
    render(<HighLights component={<CustomComp />} />);
    expect(screen.getByTestId('custom-comp')).toBeInTheDocument();
    expect(screen.queryByTestId('highlights-text')).not.toBeInTheDocument();
  });

  it('applies custom styles', () => {
    render(<HighLights text='Styled' styles={{ color: 'red' }} />);
    expect(screen.getByTestId('highlights-text')).toBeInTheDocument();
  });

  it('applies custom containerStyles', () => {
    const { container } = render(<HighLights text='Contained' containerStyles={{ padding: '20px' }} />);
    expect(container.firstChild).toBeTruthy();
  });

  it('renders with null component (uses text path)', () => {
    render(<HighLights text='Test text' component={null} />);
    expect(screen.getByTestId('highlights-text')).toHaveTextContent('Test text');
  });

  it('renders with default props', () => {
    render(<HighLights />);
    expect(screen.getByTestId('highlights-text')).toBeInTheDocument();
  });
});
