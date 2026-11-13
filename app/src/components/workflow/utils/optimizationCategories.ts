import { isCloudProvider } from './eventFilter';

// Canonical recommendation categories, matching the Optimize Recommendations page
// (CATEGORY_FILTER_OPTIONS / WIDGET_CATEGORIES). Values must match the DB `category`
// verbatim — the backend matches the trigger's `categories` param against event.category.
export const OPTIMIZATION_CATEGORY_OPTIONS = [
  { label: 'Right Sizing', value: 'RightSizing' },
  { label: 'Infra Upgrade', value: 'InfraUpgrade' },
  { label: 'Config', value: 'Configuration' },
  { label: 'Spot Instance', value: 'K8sSpotRecommendation' },
];

/**
 * Category options for an optimization trigger, scoped to the selected cluster's source.
 * Spot recommendations are produced for Kubernetes accounts only (api-server
 * k8s_recommendation_service.processSpotInstanceRecommendations reads k8s_* tables;
 * cloud-collector never emits the category), so a cloud cluster hides Spot Instance.
 * A saved category the filter drops is appended back, so editing the trigger still
 * renders — and doesn't silently lose — the saved value.
 */
export const optimizationCategoryOptionsFor = (clusterProvider: string | undefined, savedCategory: string): { label: string; value: string }[] => {
  const applicable = isCloudProvider(clusterProvider)
    ? OPTIMIZATION_CATEGORY_OPTIONS.filter((o) => o.value !== 'K8sSpotRecommendation')
    : OPTIMIZATION_CATEGORY_OPTIONS;
  if (savedCategory && !applicable.some((o) => o.value === savedCategory)) {
    const label = OPTIMIZATION_CATEGORY_OPTIONS.find((o) => o.value === savedCategory)?.label || savedCategory;
    return [...applicable, { label, value: savedCategory }];
  }
  return applicable;
};

export const isOptimizationCategoryAllowedFor = (clusterProvider: string | undefined, category: string): boolean =>
  category !== 'K8sSpotRecommendation' || !isCloudProvider(clusterProvider);
