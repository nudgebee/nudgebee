import { isUnestimatedNativeCostFinding } from '../utils';
import { buildInterpretation } from '../interpretation/buildInterpretation';

// A provider-native recommender can call a finding "cost" without attaching a
// number. Such rows stay on the Cost tab but must not read as a blank saving.
describe('provider-native cost findings without a savings figure', () => {
  it('flags a Cost-tab native row that carries no savings', () => {
    expect(isUnestimatedNativeCostFinding('RightSizing', 'gcp_native_container_diagnosis', 0)).toBe(true);
    expect(isUnestimatedNativeCostFinding('RightSizing', 'azure_native_advisor_cost', null)).toBe(true);
    expect(isUnestimatedNativeCostFinding('RightSizing', 'azure_native_advisor_cost', undefined)).toBe(true);
  });

  it('leaves priced, non-native, or non-cost rows alone', () => {
    expect(isUnestimatedNativeCostFinding('RightSizing', 'gcp_native_container_diagnosis', 12)).toBe(false);
    // A small figure is still a figure; the cell renders it as $0/mo, so the
    // detail line must not claim the provider gave none.
    expect(isUnestimatedNativeCostFinding('RightSizing', 'azure_native_advisor_cost', 0.2)).toBe(false);
    expect(isUnestimatedNativeCostFinding('RightSizing', 'gcp_native_compute_instance_machine_type', -8)).toBe(false);
    expect(isUnestimatedNativeCostFinding('RightSizing', 'pod_right_sizing', 0)).toBe(false);
    expect(isUnestimatedNativeCostFinding('Configuration', 'gcp_native_container_diagnosis', 0)).toBe(false);
    expect(isUnestimatedNativeCostFinding('RightSizing', '', 0)).toBe(false);
  });

  it('says the figure is missing instead of "no direct cost change"', () => {
    const recData = { source: 'gcp', impact_category: 'COST', subtype: 'WORKLOAD_OVERPROVISIONED' };
    const withoutFigure = buildInterpretation({
      category: 'RightSizing',
      ruleName: 'gcp_native_container_diagnosis',
      title: 'GKE Container Diagnosis',
      description: '',
      insight: '',
      savings: 0,
      recData,
    });
    expect(withoutFigure.impact).toBe('Cost — the cloud provider flags a saving here but gives no figure.');
    expect(withoutFigure.impactIsCost).toBe(false);

    const priced = buildInterpretation({
      category: 'RightSizing',
      ruleName: 'gcp_native_container_diagnosis',
      title: 'GKE Container Diagnosis',
      description: '',
      insight: '',
      savings: 30,
      recData,
    });
    expect(priced.impact).toBe('~$30/mo estimated savings.');
    expect(priced.impactIsCost).toBe(true);
  });
});
