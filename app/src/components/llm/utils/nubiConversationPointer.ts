import { getCurrentTenant } from '@lib/auth';

// One pointer per tenant to the last conversation opened in the Nubi drawer.
// The tenant lives in the KEY, the account + session in the value — so exactly
// one (account, session) pair is held per tenant, a re-login as another tenant
// on the same browser reads a different key entirely, and a read only honours
// the pair when the account also matches the caller's current context.
const keyForTenant = (): string => {
  const tenant = getCurrentTenant();
  return `nubi_selected_conversation:${tenant?.id || tenant?.name || ''}`;
};

interface Pointer {
  accountId: string;
  sessionId: string;
}

export const readNubiConversationPointer = (accountId?: string): string => {
  if (typeof window === 'undefined' || !accountId) {
    return '';
  }
  try {
    const raw = localStorage.getItem(keyForTenant());
    if (!raw) {
      return '';
    }
    const pointer = JSON.parse(raw) as Pointer | null;
    return pointer && pointer.accountId === accountId ? pointer.sessionId || '' : '';
  } catch {
    return '';
  }
};

export const writeNubiConversationPointer = (accountId?: string, sessionId?: string): void => {
  if (typeof window === 'undefined' || !accountId || !sessionId) {
    return;
  }
  try {
    localStorage.setItem(keyForTenant(), JSON.stringify({ accountId, sessionId }));
  } catch {
    // Private mode / quota — a lost "resume last chat" is acceptable.
  }
};

export const clearNubiConversationPointer = (): void => {
  if (typeof window === 'undefined') {
    return;
  }
  try {
    localStorage.removeItem(keyForTenant());
  } catch {
    // non-fatal
  }
};
