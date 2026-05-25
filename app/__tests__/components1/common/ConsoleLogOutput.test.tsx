import React from 'react';
import { render, screen } from '@testing-library/react';
import ConsoleLogOutput from '@components1/common/ConsoleLogOutput';

describe('ConsoleLogOutput', () => {
  it('renders without crashing', () => {
    const { container } = render(<ConsoleLogOutput data='Hello world' />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('renders plain log lines', () => {
    render(<ConsoleLogOutput data={'Line one\nLine two'} />);
    expect(screen.getByText('Line one')).toBeInTheDocument();
    expect(screen.getByText('Line two')).toBeInTheDocument();
  });

  it('renders a bullet indicator for regular lines', () => {
    const { container } = render(<ConsoleLogOutput data='Normal log line' />);
    const bullets = container.querySelectorAll('span');
    const bulletSpans = Array.from(bullets).filter((s) => s.textContent === '•');
    expect(bulletSpans.length).toBeGreaterThan(0);
  });

  it('does not render bullet for "No newer logs at this moment" line', () => {
    const { container } = render(<ConsoleLogOutput data='No newer logs at this moment' />);
    const bullets = Array.from(container.querySelectorAll('span')).filter((s) => s.textContent === '•');
    expect(bullets.length).toBe(0);
  });

  it('renders "No newer logs at this moment" text as bold', () => {
    const { container } = render(<ConsoleLogOutput data='No newer logs at this moment' />);
    const divs = container.querySelectorAll('div');
    const boldDiv = Array.from(divs).find((d) => d.style.fontWeight === 'bold');
    expect(boldDiv).toBeTruthy();
  });

  it('strips ANSI escape codes from displayed text', () => {
    const ESC = String.fromCharCode(27);
    const ansiText = `${ESC}[31mError occurred${ESC}[0m`;
    render(<ConsoleLogOutput data={ansiText} />);
    expect(screen.getByText('Error occurred')).toBeInTheDocument();
  });

  it('applies red color style to lines with red ANSI code', () => {
    const ESC = String.fromCharCode(27);
    const redLine = `${ESC}[31mThis is an error`;
    const { container } = render(<ConsoleLogOutput data={redLine} />);
    const divs = container.querySelectorAll('div');
    const redDiv = Array.from(divs).find((d) => d.style.color === 'red');
    expect(redDiv).toBeTruthy();
  });

  it('renders multiple lines correctly', () => {
    render(<ConsoleLogOutput data={'Line 1\nLine 2\nLine 3'} />);
    expect(screen.getByText('Line 1')).toBeInTheDocument();
    expect(screen.getByText('Line 2')).toBeInTheDocument();
    expect(screen.getByText('Line 3')).toBeInTheDocument();
  });

  it('renders with custom sx styles without crashing', () => {
    const { container } = render(<ConsoleLogOutput data='Styled log' sx={{ background: 'black' }} />);
    expect(container.querySelector('pre')).toBeInTheDocument();
  });
});
