import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import ChartSwitcher from '@shared/widgets/ChartSwitcher';

jest.mock('@utils/colors');

describe('ChartSwitcher', () => {
  test('renders without crashing', () => {
    const { container } = render(<ChartSwitcher isBarChart={false} leftButtonClick={jest.fn()} rightButtonClick={jest.fn()} />);
    expect(container).toBeTruthy();
  });

  test('renders two toggle buttons', () => {
    render(<ChartSwitcher isBarChart={false} leftButtonClick={jest.fn()} rightButtonClick={jest.fn()} />);
    const buttons = screen.getAllByRole('radio');
    expect(buttons.length).toBe(2);
  });

  test('calls leftButtonClick when line button clicked while bar is selected', () => {
    const leftButtonClick = jest.fn();
    const rightButtonClick = jest.fn();
    render(<ChartSwitcher isBarChart={true} leftButtonClick={leftButtonClick} rightButtonClick={rightButtonClick} />);
    const buttons = screen.getAllByRole('radio');
    fireEvent.click(buttons[0]);
    expect(leftButtonClick).toHaveBeenCalledTimes(1);
  });

  test('calls rightButtonClick when bar button clicked while line is selected', () => {
    const leftButtonClick = jest.fn();
    const rightButtonClick = jest.fn();
    render(<ChartSwitcher isBarChart={false} leftButtonClick={leftButtonClick} rightButtonClick={rightButtonClick} />);
    const buttons = screen.getAllByRole('radio');
    fireEvent.click(buttons[1]);
    expect(rightButtonClick).toHaveBeenCalledTimes(1);
  });

  test('line button is checked when isBarChart=false', () => {
    render(<ChartSwitcher isBarChart={false} leftButtonClick={jest.fn()} rightButtonClick={jest.fn()} />);
    const buttons = screen.getAllByRole('radio');
    expect(buttons[0]).toHaveAttribute('aria-checked', 'true');
    expect(buttons[1]).toHaveAttribute('aria-checked', 'false');
  });

  test('bar button is checked when isBarChart=true', () => {
    render(<ChartSwitcher isBarChart={true} leftButtonClick={jest.fn()} rightButtonClick={jest.fn()} />);
    const buttons = screen.getAllByRole('radio');
    expect(buttons[0]).toHaveAttribute('aria-checked', 'false');
    expect(buttons[1]).toHaveAttribute('aria-checked', 'true');
  });
});
