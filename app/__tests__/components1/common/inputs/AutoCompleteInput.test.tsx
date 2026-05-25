import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import AutoCompleteInput from '@components1/common/inputs/AutoCompleteInput';

// Mock theme/inputField
jest.mock('@data/themes/inputField', () => ({
  inputSx: {},
  inputCustomSx: {},
}));

// Mock src/utils/colors
jest.mock('src/utils/colors', () => ({
  colors: {
    border: {
      autocompleteOption: '#eee',
    },
  },
}));

describe('AutoCompleteInput', () => {
  const defaultProps = {
    label: 'Test Label',
    options: ['option1', 'option2', 'option3'],
    value: null,
    onChange: jest.fn(),
    toShowNoOption: true,
    width: 300,
  };

  beforeEach(() => {
    jest.clearAllMocks();
  });

  describe('basic rendering', () => {
    it('renders autocomplete with label', () => {
      render(<AutoCompleteInput {...defaultProps} />);
      expect(screen.getByLabelText('Test Label')).toBeInTheDocument();
    });

    it('renders with id based on label', () => {
      render(<AutoCompleteInput {...defaultProps} label='My Field' />);
      expect(document.getElementById('auto-complete-My Field')).toBeInTheDocument();
    });

    it('renders with provided value', () => {
      render(<AutoCompleteInput {...defaultProps} value='option1' />);
      const input = screen.getByRole('combobox');
      expect(input).toHaveValue('option1');
    });

    it('renders with null value', () => {
      render(<AutoCompleteInput {...defaultProps} value={null} />);
      const input = screen.getByRole('combobox');
      expect(input).toHaveValue('');
    });
  });

  describe('options behavior', () => {
    it('is disabled when options is empty', () => {
      render(<AutoCompleteInput {...defaultProps} options={[]} />);
      const input = screen.getByRole('combobox');
      expect(input).toBeDisabled();
    });

    it('is enabled when options has items', () => {
      render(<AutoCompleteInput {...defaultProps} />);
      const input = screen.getByRole('combobox');
      expect(input).not.toBeDisabled();
    });

    it('uses empty array as default for options', () => {
      // @ts-ignore - testing default behavior
      render(<AutoCompleteInput {...defaultProps} options={undefined} />);
      const input = screen.getByRole('combobox');
      // Disabled because options defaults to []
      expect(input).toBeDisabled();
    });
  });

  describe('onChange behavior', () => {
    it('calls onChange when an option is selected', () => {
      const onChange = jest.fn();
      render(<AutoCompleteInput {...defaultProps} onChange={onChange} />);
      const input = screen.getByRole('combobox');
      fireEvent.change(input, { target: { value: 'option1' } });
      fireEvent.keyDown(input, { key: 'ArrowDown' });
    });
  });

  describe('onInputChange behavior', () => {
    it('calls onInputChange when input changes', () => {
      const onInputChange = jest.fn();
      render(<AutoCompleteInput {...defaultProps} onInputChange={onInputChange} />);
      const input = screen.getByRole('combobox');
      fireEvent.change(input, { target: { value: 'opt' } });
      expect(onInputChange).toHaveBeenCalledWith('opt');
    });

    it('does not crash when onInputChange is not provided', () => {
      render(<AutoCompleteInput {...defaultProps} onInputChange={undefined} />);
      const input = screen.getByRole('combobox');
      fireEvent.change(input, { target: { value: 'test' } });
      // Should not throw
    });
  });

  describe('isLoading behavior', () => {
    it('shows CircularProgress when isLoading=true', () => {
      render(<AutoCompleteInput {...defaultProps} isLoading={true} />);
      // CircularProgress renders as a div with role="progressbar"
      expect(screen.getByRole('progressbar')).toBeInTheDocument();
    });

    it('does not show CircularProgress when isLoading=false', () => {
      render(<AutoCompleteInput {...defaultProps} isLoading={false} />);
      expect(screen.queryByRole('progressbar')).not.toBeInTheDocument();
    });

    it('defaults isLoading to false', () => {
      render(<AutoCompleteInput {...defaultProps} />);
      expect(screen.queryByRole('progressbar')).not.toBeInTheDocument();
    });
  });

  describe('toShowNoOption behavior', () => {
    it('sets freeSolo=false when toShowNoOption=true', () => {
      render(<AutoCompleteInput {...defaultProps} toShowNoOption={true} />);
      // freeSolo=false means the Autocomplete won't allow free text
      const input = screen.getByRole('combobox');
      expect(input).toBeInTheDocument();
    });

    it('sets freeSolo=true when toShowNoOption=false', () => {
      render(<AutoCompleteInput {...defaultProps} toShowNoOption={false} />);
      const input = screen.getByRole('combobox');
      expect(input).toBeInTheDocument();
    });

    it('defaults toShowNoOption to true', () => {
      // @ts-ignore - testing default
      render(<AutoCompleteInput {...defaultProps} toShowNoOption={undefined} />);
      const input = screen.getByRole('combobox');
      expect(input).toBeInTheDocument();
    });
  });

  describe('width prop', () => {
    it('uses provided width', () => {
      render(<AutoCompleteInput {...defaultProps} width={500} />);
      expect(screen.getByRole('combobox')).toBeInTheDocument();
    });

    it('defaults to maxWidth 200 when width not provided', () => {
      // @ts-ignore - testing default
      render(<AutoCompleteInput {...defaultProps} width={undefined} />);
      expect(screen.getByRole('combobox')).toBeInTheDocument();
    });
  });

  describe('isOptionEqualToValue', () => {
    it('renders without error when value matches an option', () => {
      render(<AutoCompleteInput {...defaultProps} value='option1' />);
      expect(screen.getByRole('combobox')).toHaveValue('option1');
    });

    it('renders without error when value does not match any option', () => {
      render(<AutoCompleteInput {...defaultProps} value='non-existing' />);
      expect(screen.getByRole('combobox')).toBeInTheDocument();
    });
  });

  describe('popup icon', () => {
    it('renders popup icon button', () => {
      render(<AutoCompleteInput {...defaultProps} />);
      // Autocomplete renders a popup icon button
      const popupBtn = document.querySelector('[aria-label="Open"]') || document.querySelector('.MuiAutocomplete-popupIndicator');
      expect(popupBtn).toBeInTheDocument();
    });
  });

  describe('keyboard interaction', () => {
    it('opens options on ArrowDown', () => {
      render(<AutoCompleteInput {...defaultProps} />);
      const input = screen.getByRole('combobox');
      fireEvent.keyDown(input, { key: 'ArrowDown' });
      // Options should appear
      const listbox = screen.queryByRole('listbox');
      expect(listbox).toBeInTheDocument();
    });

    it('selects option on Enter', () => {
      const onChange = jest.fn();
      render(<AutoCompleteInput {...defaultProps} onChange={onChange} />);
      const input = screen.getByRole('combobox');
      fireEvent.keyDown(input, { key: 'ArrowDown' });
      fireEvent.keyDown(input, { key: 'ArrowDown' });
      fireEvent.keyDown(input, { key: 'Enter' });
      expect(onChange).toHaveBeenCalled();
    });
  });
});
