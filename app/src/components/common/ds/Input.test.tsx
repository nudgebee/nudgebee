import React, { useState } from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { Input } from './Input';

// Emotion inserts rules via CSSOM in jsdom; gather them to inspect selectors.
const injectedCss = () =>
  Array.from(document.querySelectorAll('style'))
    .flatMap((el) => Array.from(el.sheet?.cssRules ?? []).map((r) => r.cssText))
    .join('\n');

const autofillRules = (css: string) => css.match(/[^{}]*:-webkit-autofill[^{}]*\{[^}]*\}/g) ?? [];

describe('Input autofill styling', () => {
  it('keeps the design-system fill when the browser autofills the field', () => {
    render(<Input label='K8s Secret' value='pg-secret' onChange={() => {}} size='sm' />);
    expect(screen.getByLabelText('K8s Secret')).toHaveValue('pg-secret');

    const rules = autofillRules(injectedCss());
    const base = rules.find((r) => !r.includes(':focus'));
    expect(base).toMatch(/box-shadow:\s*inset 0 0 0 1000px var\(--ds-background-100\)/);
    expect(base).toMatch(/-webkit-text-fill-color:\s*var\(--ds-gray-700\)/);
  });

  it('keeps the focus ring on an autofilled field', () => {
    render(<Input label='Name' value='postgresql' onChange={() => {}} />);
    const focusRule = autofillRules(injectedCss()).find((r) => r.includes(':focus'));
    expect(focusRule).toMatch(/inset 0 0 0 1000px var\(--ds-background-100\),\s*0 0 0 3px var\(--ds-blue-100\)/);
  });

  it('uses the disabled fill for a disabled autofilled field', () => {
    render(<Input label='Name' value='postgresql' onChange={() => {}} disabled />);
    const rules = autofillRules(injectedCss());
    expect(rules.some((r) => /inset 0 0 0 1000px var\(--ds-background-200\)/.test(r) && /--ds-gray-500/.test(r))).toBe(true);
  });

  it('keeps the error focus ring on an autofilled field', () => {
    render(<Input label='Name' value='postgresql' onChange={() => {}} error='Required' />);
    const rules = autofillRules(injectedCss());
    expect(rules.some((r) => r.includes(':focus') && /0 0 0 3px var\(--ds-red-100\)/.test(r))).toBe(true);
  });
});

describe('Input with an icon that depends on its value', () => {
  // Regression (#42088): the icon wrapper was only rendered while an icon was
  // set, so the first keystroke that made the icon appear remounted the
  // <input>: focus dropped and every later key was lost.
  const Harness = ({ prop }: { prop: 'leadingIcon' | 'trailingIcon' }) => {
    const [value, setValue] = useState('');
    const icon = value ? <span data-testid='status-icon' /> : undefined;
    return <Input label='Url' value={value} onChange={setValue} {...{ [prop]: icon }} />;
  };

  it.each(['trailingIcon', 'leadingIcon'] as const)('keeps the same focused input when the %s appears and goes', (prop) => {
    render(<Harness prop={prop} />);
    const input = screen.getByLabelText('Url');
    input.focus();

    fireEvent.change(input, { target: { value: 'h' } });
    expect(screen.getByTestId('status-icon')).toBeInTheDocument();
    expect(screen.getByLabelText('Url')).toBe(input);
    expect(input).toHaveFocus();

    fireEvent.change(input, { target: { value: 'https://example.com/health' } });
    expect(input).toHaveValue('https://example.com/health');

    fireEvent.change(input, { target: { value: '' } });
    expect(screen.queryByTestId('status-icon')).not.toBeInTheDocument();
    expect(screen.getByLabelText('Url')).toBe(input);
    expect(input).toHaveFocus();
  });
});
