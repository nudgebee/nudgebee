import { isEmailExplicitlyUnverified, isUnlinkedOAuthIdentity, shouldRejectUnverifiedOAuthEmail } from '@lib/oauthEmailVerification';

const oauth = { type: 'oauth', providerAccountId: 'idp-sub-123' };
const unlinkedProfileUser = { id: 'idp-sub-123', email: 'member@corp.com' };
const linkedAdapterUser = { id: 'nb-user-uuid', email: 'member@corp.com' };

describe('isEmailExplicitlyUnverified', () => {
  it.each([
    [{ email_verified: false }, true],
    [{ email_verified: 'false' }, true],
    [{ email_verified: ' FALSE ' }, true],
    [{ email_verified: true }, false],
    [{ email_verified: 'true' }, false],
    [{}, false],
    [{ email_verified: undefined }, false],
    [{ email_verified: null }, false],
    [null, false],
    [undefined, false],
  ])('%p -> %p', (profile, expected) => {
    expect(isEmailExplicitlyUnverified(profile)).toBe(expected);
  });
});

describe('isUnlinkedOAuthIdentity', () => {
  it('is true for the provider profile (id is the IdP subject)', () => {
    expect(isUnlinkedOAuthIdentity(unlinkedProfileUser, oauth)).toBe(true);
  });
  it('is false for the adapter user of an already-linked IdP account', () => {
    expect(isUnlinkedOAuthIdentity(linkedAdapterUser, oauth)).toBe(false);
  });
  it('is false when ids are missing', () => {
    expect(isUnlinkedOAuthIdentity({}, oauth)).toBe(false);
    expect(isUnlinkedOAuthIdentity(unlinkedProfileUser, { type: 'oauth' })).toBe(false);
    expect(isUnlinkedOAuthIdentity(null, null)).toBe(false);
  });
});

describe('shouldRejectUnverifiedOAuthEmail', () => {
  it('rejects a not-yet-linked IdP identity whose email the IdP says is unverified', () => {
    expect(shouldRejectUnverifiedOAuthEmail({ user: unlinkedProfileUser, account: oauth, profile: { email_verified: false } })).toBe(true);
  });

  it('never rejects an already-linked IdP account, whatever the email claim says', () => {
    expect(shouldRejectUnverifiedOAuthEmail({ user: linkedAdapterUser, account: oauth, profile: { email_verified: false } })).toBe(false);
  });

  it('allows a first link when the email is verified or the IdP sends no claim (Azure AD)', () => {
    expect(shouldRejectUnverifiedOAuthEmail({ user: unlinkedProfileUser, account: oauth, profile: { email_verified: true } })).toBe(false);
    expect(shouldRejectUnverifiedOAuthEmail({ user: unlinkedProfileUser, account: oauth, profile: { email: 'member@corp.com' } })).toBe(false);
  });

  it('ignores non-OAuth sign-ins', () => {
    for (const type of ['email', 'credentials']) {
      expect(shouldRejectUnverifiedOAuthEmail({ user: unlinkedProfileUser, account: { ...oauth, type }, profile: { email_verified: false } })).toBe(
        false
      );
    }
  });
});
