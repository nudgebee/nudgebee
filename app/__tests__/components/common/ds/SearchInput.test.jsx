import { render, screen, fireEvent } from '@testing-library/react';
import SearchInput from '@ui/SearchInput';

jest.mock('@ui/Input', () => ({
  Input: ({ placeholder, value, onChange, onKeyDown, trailingIcon, disabled, id }) => (
    <div>
      <input
        id={id}
        placeholder={placeholder}
        value={value}
        onChange={(e) => onChange?.(e.target.value)}
        onKeyDown={onKeyDown}
        disabled={disabled}
        data-testid='search-input'
      />
      {trailingIcon && <span data-testid='trailing-icon'>{trailingIcon}</span>}
    </div>
  ),
}));

jest.mock('@shared/icons/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt }) => <span>{alt}</span>,
}));

jest.mock('@assets', () => ({ searchSvg: 'search.svg' }));

jest.mock('@utils/colors');

describe('SearchInput', () => {
  describe('rendering', () => {
    it('renders the input element', () => {
      render(<SearchInput value='' onChange={jest.fn()} />);
      expect(screen.getByTestId('search-input')).toBeInTheDocument();
    });

    it('shows placeholder from label prop', () => {
      render(<SearchInput label='Search items' value='' onChange={jest.fn()} />);
      expect(screen.getByPlaceholderText('Search items')).toBeInTheDocument();
    });

    it('reflects the value prop', () => {
      render(<SearchInput value='hello' onChange={jest.fn()} />);
      expect(screen.getByTestId('search-input')).toHaveValue('hello');
    });

    it('is disabled when disabled=true', () => {
      render(<SearchInput value='' onChange={jest.fn()} disabled={true} />);
      expect(screen.getByTestId('search-input')).toBeDisabled();
    });
  });

  describe('clear button', () => {
    it('shows clear icon when value is truthy', () => {
      render(<SearchInput value='text' onChange={jest.fn()} />);
      expect(screen.getByTestId('trailing-icon')).toBeInTheDocument();
    });

    it('does not show clear icon when value is empty', () => {
      render(<SearchInput value='' onChange={jest.fn()} />);
      expect(screen.queryByTestId('trailing-icon')).not.toBeInTheDocument();
    });
  });

  describe('callbacks', () => {
    it('calls onChange when input value changes', () => {
      const onChange = jest.fn();
      render(<SearchInput value='' onChange={onChange} />);
      fireEvent.change(screen.getByTestId('search-input'), { target: { value: 'abc' } });
      expect(onChange).toHaveBeenCalledWith('abc');
    });

    it('calls onEnterPress when Enter key is pressed', () => {
      const onEnterPress = jest.fn();
      render(<SearchInput value='term' onChange={jest.fn()} onEnterPress={onEnterPress} />);
      fireEvent.keyDown(screen.getByTestId('search-input'), { key: 'Enter' });
      expect(onEnterPress).toHaveBeenCalledTimes(1);
    });

    it('does not call onEnterPress for non-Enter keys', () => {
      const onEnterPress = jest.fn();
      render(<SearchInput value='term' onChange={jest.fn()} onEnterPress={onEnterPress} />);
      fireEvent.keyDown(screen.getByTestId('search-input'), { key: 'a' });
      expect(onEnterPress).not.toHaveBeenCalled();
    });
  });
});
