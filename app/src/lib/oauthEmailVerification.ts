/**
 * OAuth sign-in matches an IdP identity to a Nudgebee user by email whenever
 * the IdP account is not linked yet: the adapter's email lookup plus
 * `allowDangerousEmailAccountLinking` (an invited user's first OAuth login and
 * a second provider both depend on it), tenant routing by email domain, and
 * on-prem bootstrap. An IdP that says the email is NOT verified gives no proof
 * the caller owns that mailbox, so honouring it on those paths would let
 * anyone who registers the address at the IdP sign in as its Nudgebee owner.
 *
 * Already-linked IdP accounts are matched by the provider's subject id, not
 * the email, so they are left alone: rejecting them would lock out existing
 * users without closing any hole. An absent claim is also left alone (Azure AD
 * and Azure AD B2C do not send one).
 */

type MaybeUser = { id?: string | null } | null | undefined;
type MaybeAccount = { type?: string; providerAccountId?: string | null } | null | undefined;

/** True only when the IdP explicitly asserts the email is unverified. */
export function isEmailExplicitlyUnverified(profile: unknown): boolean {
  if (!profile || typeof profile !== 'object') return false;
  const claim = (profile as { email_verified?: unknown }).email_verified;
  return claim === false || (typeof claim === 'string' && claim.trim().toLowerCase() === 'false');
}

/**
 * NextAuth hands the signIn callback the adapter user when the IdP account is
 * already linked, and the provider profile otherwise. A profile's id is the
 * IdP subject, which is also `account.providerAccountId`; a linked adapter
 * user's id is the Nudgebee user id.
 */
export function isUnlinkedOAuthIdentity(user: MaybeUser, account: MaybeAccount): boolean {
  return !!user?.id && !!account?.providerAccountId && String(user.id) === String(account.providerAccountId);
}

/** Whether to refuse an OAuth sign-in that would be resolved by an unverified email. */
export function shouldRejectUnverifiedOAuthEmail(input: { user: MaybeUser; account: MaybeAccount; profile: unknown }): boolean {
  return input.account?.type === 'oauth' && isUnlinkedOAuthIdentity(input.user, input.account) && isEmailExplicitlyUnverified(input.profile);
}
