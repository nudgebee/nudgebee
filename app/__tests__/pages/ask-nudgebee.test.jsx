import React from 'react';
import { render, screen } from '@testing-library/react';
import AskNudgebeePage from 'src/pages/ask-nudgebee';

const mockReplace = jest.fn();
jest.mock('next/router', () => ({
  useRouter: () => ({ query: {}, isReady: true, pathname: '/ask-nudgebee', replace: mockReplace }),
}));
jest.mock('@components/llm/KubernetesLLMResponseGeneratorV2', () => () => <div>nubi chat</div>);
jest.mock('@shared/AccountGuard', () => ({ withAccountGuard: (Component) => Component }));

let mockGrantsOnly = false;
let mockGrants = [];
jest.mock('@lib/auth', () => ({
  isGrantsOnlyUser: () => mockGrantsOnly,
  hasPermission: (module, cls) => mockGrants.includes(`${module}:${cls}`),
  missingPermissionMessage: (permission) => `You need the "${permission}" permission.`,
}));

describe('ask-nudgebee page', () => {
  beforeEach(() => {
    mockReplace.mockClear();
    mockGrantsOnly = false;
    mockGrants = [];
  });

  it('renders Nubi for users on built-in roles', () => {
    render(<AskNudgebeePage />);
    expect(screen.getByText('nubi chat')).toBeInTheDocument();
    expect(mockReplace).not.toHaveBeenCalled();
  });

  it('renders Nubi for a custom-role user who holds ai_conversations:Read', () => {
    mockGrantsOnly = true;
    mockGrants = ['ai_conversations:Read'];
    render(<AskNudgebeePage />);
    expect(screen.getByText('nubi chat')).toBeInTheDocument();
    expect(mockReplace).not.toHaveBeenCalled();
  });

  it('renders a permission-denied placeholder for a custom-role user without ai_conversations:Read', () => {
    mockGrantsOnly = true;
    mockGrants = ['k8s:Read'];
    render(<AskNudgebeePage />);
    expect(screen.queryByText('nubi chat')).not.toBeInTheDocument();
    expect(screen.getByText('Permission Denied')).toBeInTheDocument();
    expect(mockReplace).not.toHaveBeenCalled();
  });
});
