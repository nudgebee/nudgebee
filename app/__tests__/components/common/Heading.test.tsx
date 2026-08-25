import React from 'react';
import { render, screen } from '@testing-library/react';
import Heading, { borderWidthMap } from '@components/common/Heading';

jest.mock('@shared/icons/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt }: { alt: string }) => React.createElement('img', { 'data-testid': 'safe-icon', alt }),
}));

describe('Heading', () => {
  describe('value rendering', () => {
    it('renders value text', () => {
      render(<Heading value='Section Title' borderWidth='md' />);
      expect(screen.getByText('Section Title')).toBeInTheDocument();
    });

    it('renders value 0 (falsy but valid via value === 0 guard)', () => {
      render(<Heading value={0} borderWidth='md' />);
      expect(screen.getByText('0')).toBeInTheDocument();
    });

    it('renders a ReactNode as value', () => {
      render(<Heading value={<strong data-testid='rich-value'>Bold</strong>} borderWidth='md' />);
      expect(screen.getByTestId('rich-value')).toBeInTheDocument();
    });

    it('does not render typography when value is empty string', () => {
      const { container } = render(<Heading value='' borderWidth='md' />);
      expect(container.querySelector('.border_text')).not.toBeInTheDocument();
    });

    it('does not render typography when value is omitted', () => {
      const { container } = render(<Heading borderWidth='zero' />);
      expect(container.querySelector('.border_text')).not.toBeInTheDocument();
    });

    it('applies border_text class to the typography element', () => {
      const { container } = render(<Heading value='Title' borderWidth='md' />);
      expect(container.querySelector('.border_text')).toBeInTheDocument();
    });

    it('always renders the outer wrapper regardless of value', () => {
      const { container } = render(<Heading value='' borderWidth='md' />);
      expect(container.firstChild).toBeInTheDocument();
    });
  });

  describe('span', () => {
    it('renders span text when span prop is provided', () => {
      render(<Heading value='Title' span='Beta' borderWidth='md' />);
      expect(screen.getByText('Beta')).toBeInTheDocument();
    });

    it('renders a ReactNode as span', () => {
      render(<Heading value='Title' span={<em data-testid='span-node'>note</em>} borderWidth='md' />);
      expect(screen.getByTestId('span-node')).toBeInTheDocument();
    });

    it('does not render span element when span is empty string', () => {
      const { container } = render(<Heading value='Title' span='' borderWidth='md' />);
      expect(container.querySelector('.border_text span')).not.toBeInTheDocument();
    });

    it('does not render span element when span is omitted', () => {
      const { container } = render(<Heading value='Title' borderWidth='md' />);
      expect(container.querySelector('.border_text span')).not.toBeInTheDocument();
    });
  });

  describe('releaseIcon', () => {
    it('renders SafeIcon with alt "Beta Icon" when releaseIcon is provided', () => {
      render(<Heading value='Title' releaseIcon='/beta.svg' borderWidth='md' />);
      const icon = screen.getByTestId('safe-icon');
      expect(icon).toBeInTheDocument();
      expect(icon).toHaveAttribute('alt', 'Beta Icon');
    });

    it('does not render SafeIcon when releaseIcon is absent', () => {
      render(<Heading value='Title' borderWidth='md' />);
      expect(screen.queryByTestId('safe-icon')).not.toBeInTheDocument();
    });
  });

  describe('borderWidthMap', () => {
    it('maps zero to 0px', () => {
      expect(borderWidthMap.zero).toBe('0px');
    });

    it('maps sm to 2px', () => {
      expect(borderWidthMap.sm).toBe('2px');
    });

    it('maps md to 3px', () => {
      expect(borderWidthMap.md).toBe('3px');
    });

    it('maps lg to 4px', () => {
      expect(borderWidthMap.lg).toBe('4px');
    });
  });
});
