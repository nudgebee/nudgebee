import React from 'react';
import { render, screen } from '@testing-library/react';
import { Form } from './Form';

// #38137: a Form.Row column sized `1fr` still takes its automatic minimum size
// from its content, so a field holding a long value (the panel editor's
// "Accounts" multi-select listing several account names) widened its own column
// and spilled the row out of the form. Every column must carry a 0 floor.
describe('Form.Row column sizing', () => {
  it('floors every column at 0 so a long value cannot widen it', () => {
    render(
      <Form.Row ratio={[1, 1]}>
        <div data-testid='left'>Account type</div>
        <div data-testid='right'>a-very-long-account-name (aws), another-long-account-name (gcp)</div>
      </Form.Row>
    );

    const row = screen.getByTestId('left').parentElement as HTMLElement;
    const style = window.getComputedStyle(row);
    expect(style.display).toBe('grid');
    expect(style.gridTemplateColumns).toBe('minmax(0, 1fr) minmax(0, 1fr)');
  });

  it('keeps the ratio while flooring each column', () => {
    render(
      <Form.Row ratio={[2, 1]}>
        <div data-testid='a'>A</div>
        <div data-testid='b'>B</div>
      </Form.Row>
    );

    const row = screen.getByTestId('a').parentElement as HTMLElement;
    expect(window.getComputedStyle(row).gridTemplateColumns).toBe('minmax(0, 2fr) minmax(0, 1fr)');
  });
});
