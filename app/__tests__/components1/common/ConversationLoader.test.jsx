import React from 'react';
import { render } from '@testing-library/react';
import ConversationLoader from '@components1/common/ConversationLoader';

jest.mock('src/utils/colors', () => ({
  colors: {
    primary: '#3B82F6',
    nudgebeeMain: '#3B82F6',
    yellow: '#F59E0B',
    clusterIndicator: '#10B981',
    error: '#EF4444',
    iconColor: '#6B7280',
    text: {
      primary: '#3B82F6',
      secondary: '#374151',
      white: '#fff',
      black: '#000',
      tertiary: '#6B7280',
      yellowLabel: '#F59E0B',
      tertiarymedium: '#6B7280',
    },
    background: {
      primaryLightest: '#EFF6FF',
      white: '#fff',
      transparent: 'transparent',
      tertiaryLightest: '#F0F9FF',
      input: '#F9FAFB',
      infoGraphic: '#F8FAFC',
      error: '#EF4444',
    },
    border: {
      secondary: '#D1D5DB',
      primary: '#3B82F6',
      success: '#22C55E',
      primaryLight: '#60A5FA',
      secondaryLight: '#E5E7EB',
      white: '#fff',
      vertical: '#E5E7EB',
    },
    button: {
      primary: '#3B82F6',
      primaryText: '#fff',
      tertiaryBorder: '#BFDBFE',
    },
  },
}));

describe('ConversationLoader', () => {
  // Use real timers for all ConversationLoader tests to avoid issues with
  // the random setTimeout + setInterval combination in the component
  test('renders without crashing', () => {
    const { container, unmount } = render(<ConversationLoader />);
    expect(container).toBeTruthy();
    unmount();
  });

  test('renders a Typography element with text content', () => {
    const { container, unmount } = render(<ConversationLoader />);
    // The component renders word spans from NUDGEBEE_WORDS
    const spans = container.querySelectorAll('span');
    expect(spans.length).toBeGreaterThan(0);
    unmount();
  });

  test('renders the dot animation element', () => {
    const { container, unmount } = render(<ConversationLoader />);
    // The component renders two spans: word and dots
    const spans = container.querySelectorAll('span');
    expect(spans.length).toBeGreaterThanOrEqual(2);
    unmount();
  });

  test('cleans up timers on unmount', () => {
    const clearIntervalSpy = jest.spyOn(global, 'clearInterval');
    const clearTimeoutSpy = jest.spyOn(global, 'clearTimeout');
    const { unmount } = render(<ConversationLoader />);
    unmount();
    // After unmount, cleanup functions should have been called
    expect(clearIntervalSpy).toHaveBeenCalled();
    expect(clearTimeoutSpy).toHaveBeenCalled();
    clearIntervalSpy.mockRestore();
    clearTimeoutSpy.mockRestore();
  });
});
