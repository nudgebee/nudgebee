import React from 'react';
import { render } from '@testing-library/react';

// auth.tsx pulls in HttpService (network) and Loader (UI) — stub both so the
// module loads in isolation. `useSession` is the only input that matters here:
// withAuth() copies its `data` into the module-private `userData` that the
// permission helpers read.
jest.mock('@lib/HttpService', () => ({ queryGraphQL: jest.fn() }));
jest.mock('@shared/Loader', () => () => null);

let mockSession: { data: any; status: string } = { data: {}, status: 'authenticated' };
jest.mock('next-auth/react', () => ({
  useSession: () => mockSession,
}));

import { withAuth, hasReadAccess } from '@lib/auth';

// Render withAuth(...) once to push `session` into the module-global userData.
function applySession(data: any) {
  mockSession = { data, status: 'authenticated' };
  const Probe = withAuth(() => null);
  render(<Probe />);
}

const ACCOUNT = 'acc-1';
const OTHER_ACCOUNT = 'acc-2';

describe('hasReadAccess — account-level read for scoped roles (#32887)', () => {
  it('grants account-level read to a k8s_namespace_admin of that account (no namespace requested)', () => {
    applySession({
      roles: ['k8s_namespace_admin'],
      accountIds: [],
      readOnlyAccountIds: [],
      namespacedAccountIds: [ACCOUNT],
      namespacedReadOnlyAccountIds: [],
      k8sNamespaces: { [ACCOUNT]: ['default'] },
    });
    expect(hasReadAccess(ACCOUNT)).toBe(true);
  });

  it('grants account-level read to a k8s_namespace_admin_readonly of that account', () => {
    applySession({
      roles: ['k8s_namespace_admin_readonly'],
      accountIds: [],
      readOnlyAccountIds: [],
      namespacedAccountIds: [],
      namespacedReadOnlyAccountIds: [ACCOUNT],
      k8sNamespaces: { [ACCOUNT]: ['default'] },
    });
    expect(hasReadAccess(ACCOUNT)).toBe(true);
  });

  it('still grants account-level read to an account_admin (unchanged behaviour)', () => {
    applySession({
      roles: ['account_admin'],
      accountIds: [ACCOUNT],
      readOnlyAccountIds: [],
      namespacedAccountIds: [],
      namespacedReadOnlyAccountIds: [],
      k8sNamespaces: {},
    });
    expect(hasReadAccess(ACCOUNT)).toBe(true);
  });

  it('does not grant read to an account the user has no scope over', () => {
    applySession({
      roles: ['k8s_namespace_admin'],
      accountIds: [],
      readOnlyAccountIds: [],
      namespacedAccountIds: [ACCOUNT],
      namespacedReadOnlyAccountIds: [],
      k8sNamespaces: { [ACCOUNT]: ['default'] },
    });
    expect(hasReadAccess(OTHER_ACCOUNT)).toBe(false);
  });

  it('still enforces the per-namespace check when a specific namespace IS requested', () => {
    applySession({
      roles: ['k8s_namespace_admin'],
      accountIds: [],
      readOnlyAccountIds: [],
      namespacedAccountIds: [ACCOUNT],
      namespacedReadOnlyAccountIds: [],
      k8sNamespaces: { [ACCOUNT]: ['default'] },
    });
    // Allowed namespace passes, a namespace outside the grant is denied.
    expect(hasReadAccess(ACCOUNT, 'default')).toBe(true);
    expect(hasReadAccess(ACCOUNT, 'kube-system')).toBe(false);
  });
});
