import { resolveApprovalOptions } from '@components/workflow/utils/approvalOptions';

describe('resolveApprovalOptions', () => {
  it('prefers the options as rendered for the run over the definition', () => {
    // The regression this guards: the builder canvas read only the definition,
    // where a templated approval_options is still a string, and rendered
    // "No approval_options configured on this task." for a run whose options
    // had resolved to a real list server-side.
    const rendered = { approval_options: ['vpc-0f93095a84b1349c5', 'vpc-06f8c85f523a3e52c', 'cancel'] };
    const configured = { approval_options: "{{ Tasks['vpc-options'].output.data }}" };

    expect(resolveApprovalOptions(rendered, configured)).toEqual(['vpc-0f93095a84b1349c5', 'vpc-06f8c85f523a3e52c', 'cancel']);
  });

  it('falls back to the definition when the run has not rendered params yet', () => {
    expect(resolveApprovalOptions(undefined, { approval_options: ['approve', 'reject'] })).toEqual(['approve', 'reject']);
  });

  it('drops non-string and empty entries', () => {
    expect(resolveApprovalOptions({ approval_options: ['yes', '', 3, null, 'no'] }, undefined)).toEqual(['yes', 'no']);
  });

  it('offers the backend default when approval_options is unset — #38377', () => {
    // approval_task.go defaults to ["approve","reject"] and the completion
    // endpoint skips its allow-list check when the rendered input carries no
    // options, so these runs are approvable; offering no buttons is what left
    // them stuck RUNNING until cancelled.
    expect(resolveApprovalOptions({ message: 'ok' }, { approval_type: 'in_app' })).toEqual(['approve', 'reject']);
    expect(resolveApprovalOptions(undefined, undefined)).toEqual(['approve', 'reject']);
    expect(resolveApprovalOptions({ approval_options: [] }, {})).toEqual(['approve', 'reject']);
  });

  it('falls back to the default rather than the unresolved template text', () => {
    expect(resolveApprovalOptions(undefined, { approval_options: "{{ Tasks['x'].output.data }}" })).toEqual(['approve', 'reject']);
  });
});
