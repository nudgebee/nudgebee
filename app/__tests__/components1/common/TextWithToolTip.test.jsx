import React from 'react';
import { render, screen } from '@testing-library/react';
import TextWithToolTip from '@components1/common/TextWithToolTip';

describe('TextWithToolTip', () => {
  it('renders full text when text length is equal to default size (30)', () => {
    const text = 'a'.repeat(30);
    render(<TextWithToolTip text={text} />);
    expect(screen.getByText(text)).toBeInTheDocument();
  });

  it('renders full text when text length is less than default size (30)', () => {
    const text = 'Hello World';
    render(<TextWithToolTip text={text} />);
    expect(screen.getByText(text)).toBeInTheDocument();
  });

  it('truncates text at 30 chars with ellipsis when text is longer than default size', () => {
    const text = 'a'.repeat(35);
    render(<TextWithToolTip text={text} />);
    expect(screen.getByText(`${'a'.repeat(30)}...`)).toBeInTheDocument();
  });

  it('renders "-" when text is undefined', () => {
    render(<TextWithToolTip text={undefined} />);
    expect(screen.getByText('-')).toBeInTheDocument();
  });

  it('renders "-" when text is null', () => {
    render(<TextWithToolTip text={null} />);
    expect(screen.getByText('-')).toBeInTheDocument();
  });

  it('renders "-" when text is empty string', () => {
    render(<TextWithToolTip text='' />);
    expect(screen.getByText('-')).toBeInTheDocument();
  });

  it('renders full text when text length equals custom size', () => {
    const text = 'Hello';
    render(<TextWithToolTip text={text} size={5} />);
    expect(screen.getByText('Hello')).toBeInTheDocument();
  });

  it('truncates text at custom size=5 when text is longer', () => {
    const text = 'Hello World';
    render(<TextWithToolTip text={text} size={5} />);
    expect(screen.getByText('Hello...')).toBeInTheDocument();
  });

  it('does not truncate text shorter than custom size', () => {
    const text = 'Hi';
    render(<TextWithToolTip text={text} size={5} />);
    expect(screen.getByText('Hi')).toBeInTheDocument();
  });
});
