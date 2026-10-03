import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import FieldGuidance from '@components/workflow/components/FieldGuidance';

// MarkDowns pulls in marked/DOMPurify/mermaid, none of which this component's
// contract depends on — stub it down to the text it was handed.
jest.mock('@shared/viewers/MarkDowns', () => ({
  __esModule: true,
  default: ({ data }: { data: string }) => <div data-testid='markdown'>{data}</div>,
}));

const EXAMPLES = [
  { label: 'status = "active"', value: 'status = "active"', note: 'Exact match on a field.' },
  { label: 'cpu > 80', value: 'cpu > 80' },
];

describe('FieldGuidance', () => {
  it('renders nothing when the field declares neither help nor examples', () => {
    const { container } = render(<FieldGuidance fieldName='condition' onApply={jest.fn()} />);
    expect(container).toBeEmptyDOMElement();
  });

  it('renders one chip per example and applies the raw value on click', () => {
    const onApply = jest.fn();
    render(<FieldGuidance fieldName='condition' examples={EXAMPLES} onApply={onApply} />);

    expect(screen.getByTestId('field-example-condition-0')).toBeInTheDocument();
    expect(screen.getByTestId('field-example-condition-1')).toBeInTheDocument();

    fireEvent.click(screen.getByTestId('field-example-condition-0'));
    expect(onApply).toHaveBeenCalledWith('status = "active"');
  });

  it('applies non-string example values unchanged', () => {
    const onApply = jest.fn();
    const listExample = [{ label: 'two items', value: [{ name: 'checkout' }, { name: 'payments' }] }];
    render(<FieldGuidance fieldName='list' examples={listExample} onApply={onApply} />);

    fireEvent.click(screen.getByTestId('field-example-list-0'));
    expect(onApply).toHaveBeenCalledWith([{ name: 'checkout' }, { name: 'payments' }]);
  });

  it('shows the help affordance only when help is set', () => {
    const { rerender } = render(<FieldGuidance fieldName='condition' examples={EXAMPLES} onApply={jest.fn()} />);
    expect(screen.queryByTestId('field-help-condition')).not.toBeInTheDocument();

    rerender(<FieldGuidance fieldName='condition' help='**Comparison:** `=` `!=`' examples={EXAMPLES} onApply={jest.fn()} />);
    expect(screen.getByTestId('field-help-condition')).toBeInTheDocument();
  });

  it('renders the help row on its own, with no examples', () => {
    render(<FieldGuidance fieldName='list' help='Must be a JSON array.' onApply={jest.fn()} />);
    expect(screen.getByTestId('field-help-list')).toBeInTheDocument();
    expect(screen.queryByText('Examples:')).not.toBeInTheDocument();
  });

  it('does not fire onApply for a disabled chip', () => {
    const onApply = jest.fn();
    render(<FieldGuidance fieldName='condition' examples={EXAMPLES} onApply={onApply} disabled />);

    fireEvent.click(screen.getByTestId('field-example-condition-0'));
    expect(onApply).not.toHaveBeenCalled();
  });
});
