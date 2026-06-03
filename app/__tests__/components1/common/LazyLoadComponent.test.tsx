import React from 'react';
import { render, screen, act, waitFor } from '@testing-library/react';
import LazyLoadComponent from '@components1/common/LazyLoadComponent';

let observeCallback: IntersectionObserverCallback;

beforeEach(() => {
  global.IntersectionObserver = jest.fn().mockImplementation((cb) => {
    observeCallback = cb;
    return {
      observe: jest.fn(),
      disconnect: jest.fn(),
      unobserve: jest.fn(),
    };
  }) as any;
});

afterEach(() => {
  jest.clearAllMocks();
});

const MockComponent = ({ message }: { message?: string }) => <div data-testid='loaded-component'>{message || 'Loaded!'}</div>;

const createMockComponent =
  (comp = MockComponent) =>
  () =>
    Promise.resolve({ default: comp });

describe('LazyLoadComponent', () => {
  test('renders fallback initially (before intersection)', () => {
    render(<LazyLoadComponent component={createMockComponent()} fallback={<div>Loading...</div>} />);
    expect(screen.getByText('Loading...')).toBeInTheDocument();
  });

  test('renders "Loading..." as default fallback', () => {
    render(<LazyLoadComponent component={createMockComponent()} />);
    expect(screen.getByText('Loading...')).toBeInTheDocument();
  });

  test('loads and renders component when intersection occurs', async () => {
    render(<LazyLoadComponent component={createMockComponent()} fallback={<div>Loading...</div>} />);

    await act(async () => {
      observeCallback([{ isIntersecting: true } as IntersectionObserverEntry], {} as IntersectionObserver);
    });

    await waitFor(() => {
      expect(screen.getByTestId('loaded-component')).toBeInTheDocument();
    });
  });

  test('uses custom fallback when provided', () => {
    render(<LazyLoadComponent component={createMockComponent()} fallback={<div data-testid='custom-fallback'>Custom loading...</div>} />);
    expect(screen.getByTestId('custom-fallback')).toBeInTheDocument();
  });

  test('renders custom fallback text', () => {
    render(<LazyLoadComponent component={createMockComponent()} fallback={<span>Please wait...</span>} />);
    expect(screen.getByText('Please wait...')).toBeInTheDocument();
  });

  test('passes props to loaded component', async () => {
    render(<LazyLoadComponent component={createMockComponent()} props={{ message: 'Hello from props' }} fallback={<div>Loading...</div>} />);

    await act(async () => {
      observeCallback([{ isIntersecting: true } as IntersectionObserverEntry], {} as IntersectionObserver);
    });

    await waitFor(() => {
      expect(screen.getByText('Hello from props')).toBeInTheDocument();
    });
  });
});
