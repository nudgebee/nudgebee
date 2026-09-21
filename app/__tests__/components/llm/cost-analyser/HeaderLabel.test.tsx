import React from 'react';
import { render, screen } from '@testing-library/react';
import { HeaderLabel } from '@components/llm/cost-analyser/components/HeaderLabel';

// #35789: a right-aligned numeric column's info icon rendered after the label
// unconditionally, pushing the label text left of the column's right edge —
// while the values below had no such trailing icon, so they sat flush right.
describe('HeaderLabel alignRight', () => {
  it('keeps the icon after the label by default (left/center-aligned columns)', () => {
    render(<HeaderLabel label='Tool' info='info text' />);
    // getByText resolves to the label's text-group span; its parent is the outermost
    // span whose flex-direction is controlled by alignRight.
    const outer = screen.getByText('Tool').parentElement;
    expect(getComputedStyle(outer as Element).flexDirection).not.toBe('row-reverse');
  });

  it('puts the icon before the label when alignRight is set, so the label stays flush right', () => {
    render(<HeaderLabel label='Calls' info='info text' alignRight />);
    const outer = screen.getByText('Calls').parentElement;
    expect(getComputedStyle(outer as Element).flexDirection).toBe('row-reverse');
  });

  it('still renders the label and secondary text when alignRight is set', () => {
    render(<HeaderLabel label='Duration' secondary='(p90 / max)' info='info text' alignRight />);
    expect(screen.getByText('Duration')).toBeInTheDocument();
    expect(screen.getByText('(p90 / max)')).toBeInTheDocument();
  });
});
