// Global "open the tenant switcher" seam.
//
// The SwitchTenant modal is mounted inside the page layout
// (components/common/layout) and its open state is layout-local. This tiny
// pub/sub lets any component (e.g. the cross-tenant AccountGuard banner)
// request that the switcher open, without prop-drilling or a Context wrapping
// every layout. Mirrors the established `snackbar` service
// (components/common/snackbarService.ts).
//
// In OSS builds where the EE SwitchTenant slot is stripped, no layout
// subscribes and `open()` is a harmless no-op.
class TenantSwitcherService {
  private listeners: (() => void)[] = [];

  open() {
    this.listeners.forEach((listener) => listener());
  }

  subscribe(listener: () => void) {
    this.listeners.push(listener);
    return () => {
      this.listeners = this.listeners.filter((l) => l !== listener);
    };
  }
}

export const tenantSwitcher = new TenantSwitcherService();
