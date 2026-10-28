import { isOptimizationCategoryAllowedFor, OPTIMIZATION_CATEGORY_OPTIONS, optimizationCategoryOptionsFor } from '../optimizationCategories';

const VALUES = OPTIMIZATION_CATEGORY_OPTIONS.map((o) => o.value);

describe('optimizationCategoryOptionsFor', () => {
  it('offers every category for a Kubernetes cluster, Spot Instance included', () => {
    expect(optimizationCategoryOptionsFor('K8s', '').map((o) => o.value)).toEqual(VALUES);
  });

  it('drops Spot Instance for a cloud cluster — spot recommendations are only produced for Kubernetes accounts', () => {
    for (const provider of ['AWS', 'Azure', 'GCP', 'CloudFoundry']) {
      expect(optimizationCategoryOptionsFor(provider, '').map((o) => o.value)).toEqual(['RightSizing', 'InfraUpgrade', 'Configuration']);
    }
  });

  it('keeps every category while no cluster is picked or the account list has not loaded', () => {
    expect(optimizationCategoryOptionsFor(undefined, '').map((o) => o.value)).toEqual(VALUES);
    expect(optimizationCategoryOptionsFor('', '').map((o) => o.value)).toEqual(VALUES);
  });

  it('still renders a saved Spot Instance selection on a cloud cluster, so editing does not lose it', () => {
    const options = optimizationCategoryOptionsFor('AWS', 'K8sSpotRecommendation');
    expect(options.map((o) => o.value)).toEqual(['RightSizing', 'InfraUpgrade', 'Configuration', 'K8sSpotRecommendation']);
    expect(options.find((o) => o.value === 'K8sSpotRecommendation')?.label).toBe('Spot Instance');
  });

  it('does not duplicate a saved category that the filtered list already contains', () => {
    expect(optimizationCategoryOptionsFor('AWS', 'RightSizing').map((o) => o.value)).toEqual(['RightSizing', 'InfraUpgrade', 'Configuration']);
    expect(optimizationCategoryOptionsFor('K8s', 'Configuration').map((o) => o.value)).toEqual(VALUES);
  });

  it('renders an unknown saved category verbatim rather than dropping it', () => {
    const options = optimizationCategoryOptionsFor('AWS', 'SomeLegacyCategory');
    expect(options.find((o) => o.value === 'SomeLegacyCategory')).toEqual({ label: 'SomeLegacyCategory', value: 'SomeLegacyCategory' });
  });

  it('ignores an empty saved category', () => {
    expect(optimizationCategoryOptionsFor('AWS', '')).toHaveLength(3);
  });

  it('clears a known category only when the new source excludes it', () => {
    expect(isOptimizationCategoryAllowedFor('AWS', 'K8sSpotRecommendation')).toBe(false);
    expect(isOptimizationCategoryAllowedFor('K8s', 'K8sSpotRecommendation')).toBe(true);
    expect(isOptimizationCategoryAllowedFor('AWS', 'RightSizing')).toBe(true);
    expect(isOptimizationCategoryAllowedFor('AWS', 'SomeLegacyCategory')).toBe(true);
  });
});
