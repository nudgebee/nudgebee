import React from 'react';
import { render, screen } from '@testing-library/react';
import NubiBrainNav from '@components/common/layout/NubiBrainNav';

const mockReplace = jest.fn();
let mockQuery = {};
jest.mock('next/router', () => ({
  useRouter: () => ({ query: mockQuery, isReady: true, pathname: '/ask-nudgebee', replace: mockReplace }),
}));
jest.mock('next/dynamic', () => () => () => null);
jest.mock('@components/common/icons/SafeIcon', () => () => null);
jest.mock('@hooks/useBCortexEnabled', () => ({ isOSSDeploymentMode: () => false }));
jest.mock('@lib/auth', () => ({ hasAdminSurfaceAccess: () => true }));

describe('NubiBrainNav', () => {
  beforeEach(() => {
    mockQuery = {};
    mockReplace.mockClear();
  });

  it('shows b-Cortex on the main rail and AI & Tools only on the light surface', () => {
    render(<NubiBrainNav surface='dark' />);
    expect(screen.getByTestId('nav-bcortex-btn')).toBeInTheDocument();
    expect(screen.queryByTestId('nav-nubi-ai-tools-btn')).not.toBeInTheDocument();
  });

  it("hides b-Cortex on Nubi's sub-nav but keeps AI & Tools", () => {
    render(<NubiBrainNav surface='light' showBCortex={false} />);
    expect(screen.queryByTestId('nav-bcortex-btn')).not.toBeInTheDocument();
    expect(screen.getByTestId('nav-nubi-ai-tools-btn')).toBeInTheDocument();
  });

  it('leaves the ?bcortex= deep link to the instance that shows b-Cortex', () => {
    mockQuery = { bcortex: 'digests' };
    render(<NubiBrainNav surface='light' showBCortex={false} />);
    expect(mockReplace).not.toHaveBeenCalled();

    render(<NubiBrainNav surface='dark' />);
    expect(mockReplace).toHaveBeenCalledTimes(1);
  });

  it('drops a knowledge base deep link from the address once it is consumed', () => {
    mockQuery = { bcortex: 'knowledge-base', kb: 'kb-1', accountId: 'acct-1' };
    render(<NubiBrainNav surface='dark' />);
    expect(mockReplace).toHaveBeenCalledWith({ pathname: '/ask-nudgebee', query: { accountId: 'acct-1' } }, undefined, { shallow: true });
  });
});
