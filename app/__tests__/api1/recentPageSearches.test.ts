import apiUser, { PREFERENCE_RECENT_PAGE_SEARCHES } from '@api1/user';

// The recent-search helpers read and write the one consolidated
// `nudgebee.userPreferences` blob, so each test starts from a clean store.
const TENANT = 'tenant-1';
const seed = (values: string[]) => apiUser.storeUserPreferences(PREFERENCE_RECENT_PAGE_SEARCHES, { [TENANT]: values });

describe('recent page searches', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('drops the given values and returns what is left', () => {
    seed(['/dashboards?dashboard=gone#list', '/troubleshoot#all-events/all']);
    const remaining = apiUser.removeRecentPageSearches(['/dashboards?dashboard=gone#list'], TENANT);
    expect(remaining).toEqual(['/troubleshoot#all-events/all']);
    // Persisted, not just returned — the next read must agree.
    expect(apiUser.getRecentPageSearches(TENANT)).toEqual(['/troubleshoot#all-events/all']);
  });

  it('frees the slot it held, so the capped list can take another pick', () => {
    seed(['/a#x', '/dashboards?dashboard=gone#list', '/b#x']);
    apiUser.removeRecentPageSearches(['/dashboards?dashboard=gone#list'], TENANT);
    apiUser.addRecentPageSearch('/c#x', TENANT);
    expect(apiUser.getRecentPageSearches(TENANT)).toEqual(['/c#x', '/a#x', '/b#x']);
  });

  it('leaves the list untouched when nothing matches, or nothing is asked for', () => {
    seed(['/a#x']);
    expect(apiUser.removeRecentPageSearches(['/not-there#x'], TENANT)).toEqual(['/a#x']);
    expect(apiUser.removeRecentPageSearches([], TENANT)).toEqual(['/a#x']);
    expect(apiUser.getRecentPageSearches(TENANT)).toEqual(['/a#x']);
  });

  it('does not touch another tenant s list', () => {
    apiUser.storeUserPreferences(PREFERENCE_RECENT_PAGE_SEARCHES, { [TENANT]: ['/a#x'], 'tenant-2': ['/a#x'] });
    apiUser.removeRecentPageSearches(['/a#x'], TENANT);
    expect(apiUser.getRecentPageSearches('tenant-2')).toEqual(['/a#x']);
  });
});
