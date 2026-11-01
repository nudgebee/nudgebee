import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';
import SetupPage from '@pages/setup/index';

jest.mock('src/utils/colors');

// The card is covered in depth by its own suite. This asserts the route exists
// and mounts it — the thing that breaks if the page or its import is moved.
jest.mock('@components/onboarding/SetupCenter', () => ({
  __esModule: true,
  default: () => <div data-testid='setup-center-stub' />,
}));

const mockReplace = jest.fn();
jest.mock('next/router', () => ({ useRouter: () => ({ replace: mockReplace }) }));

let gateState: { loading: boolean; setupCenterEnabled: boolean };
jest.mock('@context/SetupGateContext', () => ({ useSetupGate: () => gateState }));

beforeEach(() => {
  jest.clearAllMocks();
  gateState = { loading: false, setupCenterEnabled: true };
});

describe('/setup', () => {
  it('mounts the Setup Center and nothing else', () => {
    render(<SetupPage />);
    expect(screen.getByTestId('setup-page')).toBeInTheDocument();
    expect(screen.getByTestId('setup-center-stub')).toBeInTheDocument();
  });

  // Defense-in-depth for a stale bookmark or a typed URL: nobody is sent here
  // organically while `UI_ENABLE_SETUP_CENTER` is off, since the gate that
  // would send them here never locks — see `SetupGateContext.tsx`.
  it('redirects to /home when UI_ENABLE_SETUP_CENTER is off', async () => {
    gateState = { loading: false, setupCenterEnabled: false };
    render(<SetupPage />);
    await waitFor(() => expect(mockReplace).toHaveBeenCalledWith('/home'));
    expect(screen.queryByTestId('setup-center-stub')).not.toBeInTheDocument();
  });

  it('does not redirect, or mount the card, while the flag is still resolving', () => {
    gateState = { loading: true, setupCenterEnabled: false };
    render(<SetupPage />);
    expect(mockReplace).not.toHaveBeenCalled();
    expect(screen.queryByTestId('setup-center-stub')).not.toBeInTheDocument();
  });

  // `refresh()` flips `loading` back on after an account connects; the card
  // must stay mounted through it or its in-progress state is lost.
  it('keeps the card mounted while the gate re-reads with the flag on', () => {
    gateState = { loading: true, setupCenterEnabled: true };
    render(<SetupPage />);
    expect(screen.getByTestId('setup-center-stub')).toBeInTheDocument();
  });
});
