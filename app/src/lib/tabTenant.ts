// Which tenant this browser tab is working in, and how the tab learns that the
// session moved to another tenant underneath it.
//
// The tenant lives in the NextAuth cookie, which every tab shares; the selected
// account lives in each tab's own state. Switching tenant in one tab used to
// leave the others showing — and saving — the old tenant's accounts, while the
// gateway stamped every write with the NEW tenant from the cookie. That wrote
// rows pairing one tenant's id with another tenant's account (feature_flag).
//
// So each tab pins the tenant it first rendered with and sends it on every
// gateway request as TAB_TENANT_HEADER. /api/graphql and /api/rpc answer 409
// TENANT_CHANGED when it no longer matches the cookie, and the tab shows a
// "reload" banner (TabTenantBanner) instead of acting on the wrong tenant.
//
// Invariant: the pin only changes on a full page load. A tenant switch must
// call beginTenantSwitch() before it updates the session and must then
// navigate with window.location (SwitchTenant, accept-invite). An in-place
// switch without a reload would leave this tab answering 409 forever.
//
// Client-safe module (no server imports): rpcGateway imports the header name.

export const TAB_TENANT_HEADER = 'x-nb-tab-tenant';
export const TENANT_CHANGED_CODE = 'TENANT_CHANGED';
export const TENANT_CHANGED_MESSAGE = 'This tab is still on a tenant you switched away from in another tab. Reload the page to continue.';

export type TenantRef = { id?: string; name?: string };

const CHANNEL_NAME = 'nb-tenant';
// Fallback for browsers without BroadcastChannel: a write to this key fires a
// `storage` event in every OTHER tab of the origin.
const STORAGE_KEY = 'nb.tenant-switch';
const LOCAL_EVENT = 'nb:tenant-changed';

let pinnedTenantId: string | null = null;
// True between beginTenantSwitch() and the navigation that follows it. This
// tab is the one switching: its requests carry no pin (so the in-flight ones
// aren't refused) and it never shows the banner.
let switching = false;

export function pinTabTenant(tenantId: string | undefined) {
  // withAuth also renders on the server, where this module is shared by every
  // request: a pin there would be one user's tenant carried into the next.
  if (typeof window === 'undefined') return;
  if (!pinnedTenantId && tenantId) pinnedTenantId = tenantId;
}

export function getTabTenantId(): string | null {
  return switching ? null : pinnedTenantId;
}

export function beginTenantSwitch() {
  switching = true;
}

// Tells the other tabs that the session now points at `tenant`.
export function announceTenantSwitch(tenant: TenantRef | undefined) {
  if (typeof window === 'undefined' || !tenant?.id) return;
  const message = { id: tenant.id, name: tenant.name };
  try {
    if (typeof BroadcastChannel !== 'undefined') {
      const channel = new BroadcastChannel(CHANNEL_NAME);
      channel.postMessage(message);
      channel.close();
      return;
    }
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify({ ...message, at: Date.now() }));
  } catch {
    // Storage blocked: the other tabs still find out on focus (session refetch)
    // or from the gateway's 409.
  }
}

// Raises the banner in THIS tab when `tenant` differs from the pinned one.
export function reportSessionTenant(tenant: TenantRef | undefined) {
  if (typeof window === 'undefined' || switching || !pinnedTenantId || !tenant?.id) return;
  if (tenant.id === pinnedTenantId) return;
  window.dispatchEvent(new CustomEvent<TenantRef>(LOCAL_EVENT, { detail: { id: tenant.id, name: tenant.name } }));
}

// Calls `onChange` with the session's new tenant whenever it stops matching the
// tenant this tab pinned. Returns the unsubscribe function.
export function subscribeTenantChange(onChange: (tenant: TenantRef) => void): () => void {
  if (typeof window === 'undefined') return () => {};
  const handle = (tenant: TenantRef | undefined) => {
    if (switching || !pinnedTenantId || !tenant?.id || tenant.id === pinnedTenantId) return;
    onChange(tenant);
  };
  const onLocal = (e: Event) => handle((e as CustomEvent<TenantRef>).detail);
  const onStorage = (e: StorageEvent) => {
    if (e.key !== STORAGE_KEY || !e.newValue) return;
    try {
      handle(JSON.parse(e.newValue));
    } catch {
      // Not ours / malformed — ignore.
    }
  };
  let channel: BroadcastChannel | null = null;
  if (typeof BroadcastChannel !== 'undefined') {
    channel = new BroadcastChannel(CHANNEL_NAME);
    channel.onmessage = (e: MessageEvent<TenantRef>) => handle(e.data);
  }
  window.addEventListener(LOCAL_EVENT, onLocal);
  window.addEventListener('storage', onStorage);
  return () => {
    channel?.close();
    window.removeEventListener(LOCAL_EVENT, onLocal);
    window.removeEventListener('storage', onStorage);
  };
}

// True for a gateway response that refused this tab's stale tenant pin, in
// either envelope: GraphQL `errors[].extensions.code` or JSON-RPC `error.data.code`.
export function isTenantChangedResponse(status: number | undefined, body: any): boolean {
  if (status !== 409 || !body) return false;
  return body?.errors?.[0]?.extensions?.code === TENANT_CHANGED_CODE || body?.error?.data?.code === TENANT_CHANGED_CODE;
}

export function tenantFromChangedResponse(body: any): TenantRef | undefined {
  return body?.errors?.[0]?.extensions?.tenant ?? body?.error?.data?.tenant;
}
