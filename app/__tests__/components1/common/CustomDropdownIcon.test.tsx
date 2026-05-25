import React from 'react';
import { render } from '@testing-library/react';
import CustomDropdownIcon from '@components1/common/CustomDropdownIcon';

describe('CustomDropdownIcon', () => {
  test('renders without crashing', () => {
    const { container } = render(<CustomDropdownIcon color='#3B82F6' props={{}} />);
    expect(container).toBeTruthy();
  });

  test('renders with a color prop', () => {
    const { container } = render(<CustomDropdownIcon color='#FF0000' props={{}} />);
    expect(container.firstChild).toBeInTheDocument();
  });

  test('renders a svg element', () => {
    const { container } = render(<CustomDropdownIcon color='#3B82F6' props={{}} />);
    const svgElement = container.querySelector('svg');
    expect(svgElement).toBeInTheDocument();
  });
});
