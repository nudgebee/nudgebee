import React from 'react';
import { render, screen, fireEvent, act } from '@testing-library/react';
import ScrollToTopBottom from '@components1/common/ScrollToTopBottom';

jest.mock('react-icons/fa', () => ({
  FaArrowUp: () => <svg data-testid='arrow-up' />,
  FaArrowDown: () => <svg data-testid='arrow-down' />,
}));

describe('ScrollToTopBottom', () => {
  beforeEach(() => {
    Object.defineProperty(window, 'scrollY', { writable: true, configurable: true, value: 0 });
    Object.defineProperty(document.documentElement, 'scrollHeight', { writable: true, configurable: true, value: 2000 });
    Object.defineProperty(window, 'innerHeight', { writable: true, configurable: true, value: 800 });
    window.scrollTo = jest.fn();
  });

  afterEach(() => {
    jest.clearAllMocks();
  });

  test('renders scroll to top button with aria-label "Scroll to top"', () => {
    render(<ScrollToTopBottom />);
    expect(screen.getByRole('button', { name: 'Scroll to top' })).toBeInTheDocument();
  });

  test('renders scroll to bottom button with aria-label "Scroll to bottom"', () => {
    render(<ScrollToTopBottom />);
    expect(screen.getByRole('button', { name: 'Scroll to bottom' })).toBeInTheDocument();
  });

  test('scroll to top button calls window.scrollTo when clicked', () => {
    render(<ScrollToTopBottom />);
    const topButton = screen.getByRole('button', { name: 'Scroll to top' });
    fireEvent.click(topButton);
    expect(window.scrollTo).toHaveBeenCalledWith({ top: 0, behavior: 'smooth' });
  });

  test('scroll to bottom button calls window.scrollTo when clicked', () => {
    render(<ScrollToTopBottom />);
    const bottomButton = screen.getByRole('button', { name: 'Scroll to bottom' });
    fireEvent.click(bottomButton);
    expect(window.scrollTo).toHaveBeenCalledWith({
      top: document.documentElement.scrollHeight,
      behavior: 'smooth',
    });
  });

  test('shows buttons when user scrolls past 250px', () => {
    render(<ScrollToTopBottom />);
    act(() => {
      Object.defineProperty(window, 'scrollY', { writable: true, configurable: true, value: 300 });
      fireEvent.scroll(window);
    });
    const topButton = screen.getByRole('button', { name: 'Scroll to top' });
    expect(topButton).toBeInTheDocument();
  });

  test('bottom button with alwaysShowBottomArrow=true makes it always visible', () => {
    render(<ScrollToTopBottom alwaysShowBottomArrow={true} />);
    const bottomButton = screen.getByRole('button', { name: 'Scroll to bottom' });
    expect(bottomButton).toBeInTheDocument();
  });
});
