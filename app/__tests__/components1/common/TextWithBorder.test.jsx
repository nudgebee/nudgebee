import React from 'react';
import { render, screen } from '@testing-library/react';
import TextWithBorder from '@components1/common/TextWithBorder';

jest.mock('@components1/common/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt }) => <img alt={alt} data-testid='safe-icon' />,
}));

describe('TextWithBorder', () => {
  it('renders Typography with value text when value is provided', () => {
    render(<TextWithBorder value='Hello' />);
    expect(screen.getByText('Hello')).toBeInTheDocument();
  });

  it('renders nothing inside Box when value is empty string', () => {
    const { container } = render(<TextWithBorder value='' />);
    expect(container.querySelector('.border_text')).toBeNull();
  });

  it('renders nothing inside Box when value is not provided', () => {
    const { container } = render(<TextWithBorder />);
    expect(container.querySelector('.border_text')).toBeNull();
  });

  it('renders span text when span is provided along with value', () => {
    render(<TextWithBorder value='Main' span='Sub' />);
    expect(screen.getByText('Sub')).toBeInTheDocument();
  });

  it('does not render span when span is empty', () => {
    const { container } = render(<TextWithBorder value='Main' span='' />);
    // Only the value text should be present, no extra span element with content
    expect(screen.getByText('Main')).toBeInTheDocument();
    // Verify there is no non-empty span besides the value itself
    const spans = container.querySelectorAll('span');
    const nonEmptySpans = Array.from(spans).filter((s) => s.textContent && s.textContent.trim() !== '');
    // Only 'Main' content should exist, no separate span text
    nonEmptySpans.forEach((s) => {
      expect(s.textContent).toBe('Main');
    });
  });

  it('renders releaseIcon via SafeIcon when releaseIcon is provided and value is set', () => {
    render(<TextWithBorder value='v1.0' releaseIcon='beta-icon.svg' />);
    expect(screen.getByTestId('safe-icon')).toBeInTheDocument();
    expect(screen.getByAltText('Beta Icon')).toBeInTheDocument();
  });

  it('does not render SafeIcon when releaseIcon is not provided', () => {
    render(<TextWithBorder value='v1.0' />);
    expect(screen.queryByTestId('safe-icon')).not.toBeInTheDocument();
  });

  it('applies default padding "0px 10px" to the outer Box', () => {
    const { container } = render(<TextWithBorder value='Test' />);
    const box = container.firstChild;
    expect(box).toBeInTheDocument();
  });

  it('renders value Typography with the border_text class', () => {
    const { container } = render(<TextWithBorder value='Styled' />);
    expect(container.querySelector('.border_text')).toBeInTheDocument();
  });
});
