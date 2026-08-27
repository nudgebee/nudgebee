import React from 'react';
import { render, screen } from '@testing-library/react';
import FieldGuidance from '@components/workflow/components/FieldGuidance';

// Deliberately NOT mocking MarkDowns: the other suites stub it, so this is the
// only place the real markdown/sanitiser path for `help` is exercised.
describe('FieldGuidance help with the real markdown renderer', () => {
  it('renders help markdown without a link handler', () => {
    const help = 'Must be a JSON **array**.\n\nFull reference: https://docs.jsonata.org/predicate';
    expect(() => render(<FieldGuidance fieldName='list' help={help} onApply={jest.fn()} />)).not.toThrow();
    expect(screen.getByTestId('field-help-list')).toBeInTheDocument();
  });
});
