// Optional: configure or set up a testing framework before each test.
// If you delete this file, remove `setupFilesAfterEnv` from `jest.config.js`

// Used for __tests__/testing-library.js
// Learn more: https://github.com/testing-library/jest-dom
import React from 'react';
import '@testing-library/jest-dom';

jest.mock('next/router', () => ({
  useRouter: () => ({
    push: jest.fn(),
    replace: jest.fn(),
    pathname: '/',
    query: {},
    asPath: '/',
    route: '/',
    prefetch: jest.fn().mockResolvedValue(null),
  }),
}));

jest.mock('next/image', () => ({
  __esModule: true,
  // Strip Next.js-specific props before forwarding to DOM <img> to avoid
  // "non-boolean attribute" / "unknown prop" React warnings in tests.
  default: ({ src, alt, unoptimized: _u, onError: _e, fill: _f, priority: _p, ...props }) =>
    React.createElement('img', { src: typeof src === 'object' ? src?.src ?? '' : src, alt, ...props }),
}));

jest.mock('mermaid', () => ({
  initialize: jest.fn(),
  render: jest.fn().mockResolvedValue('svg content'),
  contentLoaded: jest.fn(),
}));

class ResizeObserver {
  constructor(callback) {
    this.callback = callback;
  }
  observe() {
    // do nothing
  }
  unobserve() {
    // do nothing
  }
  disconnect() {
    // do nothing
  }
}

global.ResizeObserver = ResizeObserver;

Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: jest.fn().mockImplementation((query) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: jest.fn(),
    removeListener: jest.fn(),
    addEventListener: jest.fn(),
    removeEventListener: jest.fn(),
    dispatchEvent: jest.fn(),
  })),
});

global.fetch = jest.fn(() =>
  Promise.resolve({
    json: () => Promise.resolve({}),
    ok: true,
    status: 200,
  })
);
