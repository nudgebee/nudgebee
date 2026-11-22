import React from 'react';
import { render, waitFor } from '@testing-library/react';
import RedirectPage from 'src/pages/index';

const mockReplace = jest.fn();
jest.mock('next/router', () => ({ useRouter: () => ({ replace: mockReplace }) }));

beforeEach(() => {
  jest.clearAllMocks();
});

describe('the root redirect', () => {
  // Was router.push() during render, unguarded — every visit to `/` painted
  // /home for a frame before the setup gate, evaluating at that same instant,
  // could object and correct it to /setup. Routing belongs in an effect.
  it('routes in an effect, not during render', () => {
    expect(() => render(<RedirectPage />)).not.toThrow();
    expect(mockReplace).toHaveBeenCalledWith('/ask-nudgebee');
  });

  // `replace`, not `push`: this page should never itself become a Back target.
  it('replaces rather than pushes', async () => {
    render(<RedirectPage />);
    await waitFor(() => expect(mockReplace).toHaveBeenCalled());
    expect(mockReplace).toHaveBeenCalledTimes(1);
  });

  it('renders nothing', () => {
    const { container } = render(<RedirectPage />);
    expect(container).toBeEmptyDOMElement();
  });
});
