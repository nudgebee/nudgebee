import React from 'react';
import { render, act } from '@testing-library/react';
import ThreeDotLoader, { ThreeDotsLoaderText } from '@components1/common/ThreeDotLoader';

describe('ThreeDotLoader', () => {
  test('renders the dot-pulse-container div', () => {
    const { container } = render(<ThreeDotLoader />);
    expect(container.querySelector('.dot-pulse-container')).toBeInTheDocument();
  });

  test('renders the dot-pulse div inside', () => {
    const { container } = render(<ThreeDotLoader />);
    expect(container.querySelector('.dot-pulse')).toBeInTheDocument();
  });
});

describe('ThreeDotsLoaderText', () => {
  beforeEach(() => {
    jest.useFakeTimers();
  });

  afterEach(() => {
    jest.useRealTimers();
  });

  test('renders a span with text-dot-loader class', () => {
    const { container } = render(<ThreeDotsLoaderText />);
    expect(container.querySelector('.text-dot-loader')).toBeInTheDocument();
  });

  test('starts with empty dots string', () => {
    const { container } = render(<ThreeDotsLoaderText />);
    const span = container.querySelector('.text-dot-loader');
    expect(span.textContent).toBe('');
  });

  test('cycles dots with fake timers', () => {
    const { container } = render(<ThreeDotsLoaderText />);
    const span = container.querySelector('.text-dot-loader');

    act(() => {
      jest.advanceTimersByTime(500);
    });

    expect(span.textContent).toBe('.');
  });
});
