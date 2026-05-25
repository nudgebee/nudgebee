import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import CustomSelectDropdown from '@components1/common/CustomSelectDropdown';

const stringOptions = ['Option A', 'Option B', 'Option C'];
const objectOptions = [
  { label: 'Item One', value: 'one' },
  { label: 'Item Two', value: 'two' },
  { label: 'Item Three', value: 'three' },
];

describe('CustomSelectDropdown', () => {
  it('renders without crashing', () => {
    const { container } = render(<CustomSelectDropdown options={stringOptions} onChange={jest.fn()} />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('renders label when provided', () => {
    render(<CustomSelectDropdown label='Status' options={stringOptions} onChange={jest.fn()} />);
    // MUI Select renders label in multiple DOM nodes (InputLabel + hidden span)
    const labelElements = screen.getAllByText('Status');
    expect(labelElements.length).toBeGreaterThan(0);
  });

  it('renders with a preselected value', () => {
    render(<CustomSelectDropdown label='Item' options={objectOptions} value='one' onChange={jest.fn()} />);
    // The select element should have the preselected value text rendered
    expect(screen.getByText('Item One')).toBeInTheDocument();
  });

  it('is disabled when isDisabled is true', () => {
    render(<CustomSelectDropdown options={stringOptions} onChange={jest.fn()} isDisabled />);
    expect(screen.getByRole('combobox')).toHaveAttribute('aria-disabled', 'true');
  });

  it('is disabled when options array is empty', () => {
    render(<CustomSelectDropdown options={[]} onChange={jest.fn()} />);
    expect(screen.getByRole('combobox')).toHaveAttribute('aria-disabled', 'true');
  });

  it('calls onChange when an option is selected', async () => {
    const onChange = jest.fn();
    render(<CustomSelectDropdown options={objectOptions} value='' onChange={onChange} label='Pick' />);
    fireEvent.mouseDown(screen.getByRole('combobox'));
    await waitFor(() => {
      expect(screen.getByText('Item One')).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText('Item One'));
    expect(onChange).toHaveBeenCalled();
  });

  it('shows "All" option when showAll is true', async () => {
    render(<CustomSelectDropdown options={stringOptions} onChange={jest.fn()} showAll />);
    fireEvent.mouseDown(screen.getByRole('combobox'));
    await waitFor(() => {
      expect(screen.getByText('All')).toBeInTheDocument();
    });
  });

  it('does not show "All" option when showAll is false', async () => {
    render(<CustomSelectDropdown options={stringOptions} onChange={jest.fn()} showAll={false} />);
    fireEvent.mouseDown(screen.getByRole('combobox'));
    await waitFor(() => {
      expect(screen.queryByText('All')).not.toBeInTheDocument();
    });
  });
});
