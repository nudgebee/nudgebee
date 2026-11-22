import { remediationLinkText, splitAtNextSteps } from '../remediationLink';

describe('remediationLink', () => {
  it('cuts the report just before its Next Steps heading, whatever its level or emoji', () => {
    expect(splitAtNextSteps('A\n\n### 🧭 Next Steps\n- x')).toEqual({ before: 'A\n\n', after: '### 🧭 Next Steps\n- x' });
    expect(splitAtNextSteps('## Next steps\n1. y')).toEqual({ before: '', after: '## Next steps\n1. y' });
    expect(splitAtNextSteps('The next steps are below.\n- x')).toBeNull();
    expect(splitAtNextSteps(null)).toBeNull();
  });

  it('says how many fixes are ready, or that one is being prepared', () => {
    expect(remediationLinkText({ count: 1 }, 'Nubi')).toBe('Nubi made these runnable → Remediation (1 fix ready)');
    expect(remediationLinkText({ count: 3 }, 'Nubi')).toBe('Nubi made these runnable → Remediation (3 fixes ready)');
    expect(remediationLinkText({ count: 0, preparing: true }, 'Nubi')).toBe('Nubi is preparing a runnable fix → Remediation');
    expect(remediationLinkText({ count: 0 }, 'Nubi')).toBe('');
    expect(remediationLinkText(null)).toBe('');
  });
});
