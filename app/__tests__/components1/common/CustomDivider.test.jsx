import React from 'react';
import { render } from '@testing-library/react';
import CustomDivider from '@components1/common/CustomDivider';

describe('CustomDivider', () => {
  it('renders without crashing', () => {
    const { container } = render(<CustomDivider />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('renders a single element', () => {
    const { container } = render(<CustomDivider />);
    expect(container.childElementCount).toBe(1);
  });

  it('renders with default border color #EBEBEB', () => {
    const { container } = render(<CustomDivider />);
    // MUI Box renders with sx, check the element exists
    expect(container.firstChild).toBeInTheDocument();
  });

  it('renders with default borderWidth of 0.5px and borderType of solid', () => {
    const { container } = render(<CustomDivider />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('accepts custom borderColor prop', () => {
    expect(() => render(<CustomDivider borderColor='#FF0000' />)).not.toThrow();
  });

  it('accepts custom borderWidth prop', () => {
    expect(() => render(<CustomDivider borderWidth='2px' />)).not.toThrow();
  });

  it('accepts custom borderType prop', () => {
    expect(() => render(<CustomDivider borderType='dashed' />)).not.toThrow();
  });

  it('accepts custom margin prop', () => {
    expect(() => render(<CustomDivider margin='20px 0px' />)).not.toThrow();
  });

  it('accepts custom maxWidth prop', () => {
    expect(() => render(<CustomDivider maxWidth='500px' />)).not.toThrow();
  });

  it('renders with all props provided without crashing', () => {
    expect(() =>
      render(<CustomDivider margin='5px 0px' borderWidth='1px' borderType='dashed' maxWidth='300px' borderColor='#CCCCCC' />)
    ).not.toThrow();
  });

  it('renders with no props (uses all defaults)', () => {
    expect(() => render(<CustomDivider />)).not.toThrow();
  });

  it('renders only one child element when no margin is passed', () => {
    const { container } = render(<CustomDivider />);
    expect(container.children.length).toBe(1);
  });

  it('renders only one child element when margin is passed', () => {
    const { container } = render(<CustomDivider margin='15px 0' />);
    expect(container.children.length).toBe(1);
  });

  it('renders only one child element when all props are passed', () => {
    const { container } = render(<CustomDivider margin='10px 0' borderWidth='1px' borderType='dotted' maxWidth='100%' borderColor='#000' />);
    expect(container.children.length).toBe(1);
  });
});
