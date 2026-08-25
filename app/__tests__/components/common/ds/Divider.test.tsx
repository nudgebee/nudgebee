import React from 'react';
import { render } from '@testing-library/react';
import { Divider } from '@ui/Divider';

describe('Divider', () => {
  it('renders a horizontal separator by default', () => {
    const { container } = render(<Divider />);
    const sep = container.querySelector('[role="separator"]');
    expect(sep).toBeInTheDocument();
  });

  it('renders a vertical separator with aria-orientation vertical', () => {
    const { container } = render(<Divider orientation='vertical' />);
    const sep = container.querySelector('[role="separator"]');
    expect(sep).toBeInTheDocument();
    expect(sep).toHaveAttribute('aria-orientation', 'vertical');
  });
});
