/**
 * Guards the first-visit offer table. The failure mode here is silent: a section
 * whose `path` doesn't match `router.pathname`, or whose `landingFragments` miss
 * the fragment the page actually lands on, simply never offers its guide — no
 * error, nothing to notice. So these pin the wiring against the real registry and
 * the real routes.
 */
import { TOURS } from '@components/common/tour/tours';
import { SECTION_TOURS, matchesSection } from '@components/onboarding/SectionFirstVisitTour';

describe('SECTION_TOURS wiring', () => {
  it.each(SECTION_TOURS.map((s) => [s.tourId, s] as const))('%s exists in the tour registry', (tourId) => {
    expect(TOURS[tourId]).toBeDefined();
  });

  it('gives every section a distinct preference key', () => {
    const prefs = SECTION_TOURS.map((s) => s.pref);
    expect(new Set(prefs).size).toBe(prefs.length);
  });

  it('gives every section a welcome card, since that is what the offer renders', () => {
    SECTION_TOURS.forEach((s) => {
      expect(s.welcome?.title).toBeTruthy();
      expect(s.welcome?.description).toBeTruthy();
    });
  });

  it("points each section at its guide's own route", () => {
    // A section whose path disagrees with the guide's route would offer the guide
    // somewhere its anchors don't exist. Cloud is the deliberate exception: its
    // route is the /cloud-account redirector, but the offer must fire on the
    // details page the redirect lands on.
    SECTION_TOURS.filter((s) => s.tourId !== 'cloud-overview').forEach((s) => {
      expect(TOURS[s.tourId].route?.split('#')[0]).toBe(s.path);
    });
    expect(TOURS['cloud-overview'].route).toBe('/cloud-account');
    expect(SECTION_TOURS.find((s) => s.tourId === 'cloud-overview')?.path).toBe('/cloud-account/details/[CloudAccountDetails]');
  });
});

describe('matchesSection', () => {
  const section = (over: Record<string, unknown> = {}) => ({ path: '/optimise', tourId: 't', pref: 'p', ...over } as any);

  it('requires the pathname to match exactly', () => {
    expect(matchesSection(section(), '/optimise', '')).toBe(true);
    expect(matchesSection(section(), '/troubleshoot', '')).toBe(false);
  });

  it('matches a hash-scoped section only on its own fragment', () => {
    const kg = section({ path: '/troubleshoot', hash: 'kg' });
    expect(matchesSection(kg, '/troubleshoot', 'kg')).toBe(true);
    expect(matchesSection(kg, '/troubleshoot', 'investigations')).toBe(false);
    expect(matchesSection(kg, '/troubleshoot', '')).toBe(false);
  });

  it('matches a landing section on any fragment it declares', () => {
    const optimise = section({ landingFragments: ['', 'summary'] });
    expect(matchesSection(optimise, '/optimise', '')).toBe(true);
    expect(matchesSection(optimise, '/optimise', 'summary')).toBe(true);
    // A deep-link into another tab is not the landing view — don't offer there.
    expect(matchesSection(optimise, '/optimise', 'recommendations')).toBe(false);
  });

  it('defaults to the no-hash landing view when a section declares no fragments', () => {
    expect(matchesSection(section(), '/optimise', '')).toBe(true);
    expect(matchesSection(section(), '/optimise', 'summary')).toBe(false);
  });

  it('still treats all-events as a Troubleshoot landing view (regression: it was hardcoded)', () => {
    const ts = SECTION_TOURS.find((s) => s.tourId === 'troubleshoot-overview');
    expect(matchesSection(ts, '/troubleshoot', 'all-events')).toBe(true);
    expect(matchesSection(ts, '/troubleshoot', '')).toBe(true);
  });
});
