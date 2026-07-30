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

import {
  withAuth,
  hasReadAccess,
  hasPermission,
  canManage,
  hasAdminSurfaceAccess,
  hasAnyCustomGrant,
  isCustomRolesEnabled,
  isGrantsOnlyUser,
} from '@lib/auth';

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

// ---------------------------------------------------------------------------
// Dynamic RBAC on/off switch. `customRolesEnabled` on the session is the single
// input that decides whether any custom-role gating happens at all; with it
// false every helper must answer exactly as it did before the feature existed,
// even for a session that still carries grant keys (a JWT minted while the
// feature was on, before the bounded refresh clears it).
// ---------------------------------------------------------------------------
describe('dynamic-RBAC switch (customRolesEnabled)', () => {
  const GRANTS = { permissions: ['audits:Read', 'notifications:Write'], accountIds: [], readOnlyAccountIds: [] };

  it('honors grants when the feature is enabled', () => {
    applySession({ roles: [], customRolesEnabled: true, ...GRANTS });
    expect(isCustomRolesEnabled()).toBe(true);
    expect(hasPermission('audits', 'Read')).toBe(true);
    expect(canManage('notifications', 'Write')).toBe(true);
    expect(hasAdminSurfaceAccess()).toBe(true);
    expect(hasAnyCustomGrant()).toBe(true);
    expect(isGrantsOnlyUser()).toBe(true);
  });

  it('ignores the same grants when the feature is disabled', () => {
    applySession({ roles: [], customRolesEnabled: false, ...GRANTS });
    expect(isCustomRolesEnabled()).toBe(false);
    expect(hasPermission('audits', 'Read')).toBe(false);
    expect(canManage('notifications', 'Write')).toBe(false);
    expect(hasAdminSurfaceAccess()).toBe(false);
    expect(hasAnyCustomGrant()).toBe(false);
    // The gating layer collapses: no user is "grants-only" with the feature off,
    // so nav icons / tabs / quick links are never disabled.
    expect(isGrantsOnlyUser()).toBe(false);
  });

  it('treats a session without the field as disabled', () => {
    applySession({ roles: [], ...GRANTS });
    expect(isCustomRolesEnabled()).toBe(false);
    expect(isGrantsOnlyUser()).toBe(false);
  });

  it('leaves built-in roles authoritative in both states', () => {
    for (const customRolesEnabled of [true, false]) {
      applySession({ roles: ['tenant_admin'], customRolesEnabled, permissions: [] });
      expect(canManage('notifications', 'Write')).toBe(true);
      expect(hasAdminSurfaceAccess()).toBe(true);
      // A tenant-wide role is never grants-only, feature on or off.
      expect(isGrantsOnlyUser()).toBe(false);
    }
  });

  it('never gates the demo account', () => {
    applySession({ roles: [], customRolesEnabled: true, ...GRANTS });
    expect(isGrantsOnlyUser('demo')).toBe(false);
  });
});
