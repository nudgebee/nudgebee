// users.kind: a person, or a bee — an AI worker that acts under its own account.
// A bee holds custom roles and shows in audit like anyone else, but never signs in.
export const USER_KIND_BEE = 'bee';

export function isBeeAccount(user: { kind?: string } | null | undefined): boolean {
  return user?.kind === USER_KIND_BEE;
}

// A bee's username is `bee:<canonical name>.<random>`; it is shown as
// `<canonical name>[bee]`, the way service accounts read elsewhere (`app[bot]`).
export function beeLabel(user: { username?: string } | null | undefined): string {
  const name = (user?.username || '').replace(/^bee:/, '').replace(/\.[0-9a-f]{8}$/, '');
  return `${name}[bee]`;
}
