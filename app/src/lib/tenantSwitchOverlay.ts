// Shared between `SwitchTenant` (sets it right before the hard reload a
// tenant switch requires — see tabTenant.ts's invariant) and
// `TenantSwitchOverlay` (reads it on the fresh page load that follows, to
// mask the old page/wrong-home flash until the setup gate has actually
// settled). A sessionStorage key, not React state, because the hard reload
// tears down the whole app — nothing in memory survives it.
export const TENANT_SWITCH_FLAG = 'nb.tenant-switch-pending';
