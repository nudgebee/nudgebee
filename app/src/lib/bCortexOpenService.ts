// Global "open b-Cortex on a specific tab" seam.
//
// BCortexModal is mounted inside NubiBrainNav.jsx, whose open state is
// layout-local. This tiny pub/sub lets GlobalPageSearch.jsx request a tab
// open — e.g. from a header-search pick — without prop-drilling or a Context
// wrapping every layout. Mirrors the established `tenantSwitcher` seam
// (lib/tenantSwitcherService.ts).
//
// Where no NubiBrainNav is mounted (OSS builds, or a layout without it),
// open() is a harmless no-op.
class BCortexOpenService {
  private listeners: ((tab: string) => void)[] = [];

  open(tab: string) {
    this.listeners.forEach((listener) => listener(tab));
  }

  subscribe(listener: (tab: string) => void) {
    this.listeners.push(listener);
    return () => {
      this.listeners = this.listeners.filter((l) => l !== listener);
    };
  }
}

export const bCortexOpener = new BCortexOpenService();
