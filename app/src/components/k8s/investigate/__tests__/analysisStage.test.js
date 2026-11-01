import { describeAnalysisStage } from '../analysisStage';

describe('describeAnalysisStage', () => {
  it('names the first step still waiting, in the order the server runs them', () => {
    expect(
      describeAnalysisStage({ summary: 'IN_PROGRESS', investigation: 'IN_PROGRESS', log_analysis: 'IN_PROGRESS', detailed_response: 'IN_PROGRESS' })
    ).toBe('Step 1 of 4 · Summarising the alert');
    expect(
      describeAnalysisStage({ detailed_response: 'IN_PROGRESS', log_analysis: 'PENDING', investigation: 'in_progress', summary: 'COMPLETED' })
    ).toBe('Step 2 of 4 · Investigating: collecting and reading the evidence');
    expect(
      describeAnalysisStage({ summary: 'COMPLETED', investigation: 'COMPLETED', log_analysis: 'FAILED', detailed_response: 'IN_PROGRESS' })
    ).toBe('Step 4 of 4 · Writing the report');
  });

  it('counts only the steps this run has', () => {
    expect(describeAnalysisStage({ summary: 'COMPLETED', investigation: 'IN_PROGRESS', detailed_response: 'IN_PROGRESS' })).toBe(
      'Step 2 of 3 · Investigating: collecting and reading the evidence'
    );
  });

  it('names a single-step run without a count', () => {
    expect(describeAnalysisStage({ summary: 'IN_PROGRESS' })).toBe('Summarising the alert');
  });

  it('is empty when nothing is waiting or the statuses are unknown', () => {
    expect(describeAnalysisStage({ summary: 'COMPLETED', investigation: 'COMPLETED', detailed_response: 'COMPLETED' })).toBe('');
    expect(describeAnalysisStage({})).toBe('');
    expect(describeAnalysisStage(null)).toBe('');
    expect(describeAnalysisStage(undefined)).toBe('');
  });
});
