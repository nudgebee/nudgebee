import { render, screen, fireEvent } from '@testing-library/react';
import BackButton from '@components/common/buttons/BackButton';

const mockPush = jest.fn();
const mockBack = jest.fn();

jest.mock('next/router', () => ({
  useRouter: jest.fn(),
}));

jest.mock('@ui/Button', () => ({
  Button: ({ 'aria-label': ariaLabel, onClick, id }) => <button aria-label={ariaLabel} id={id} onClick={onClick} data-testid='back-btn' />,
}));

import { useRouter } from 'next/router';

beforeEach(() => {
  mockPush.mockClear();
  mockBack.mockClear();
  useRouter.mockReturnValue({ push: mockPush, back: mockBack });
});

describe('BackButton', () => {
  describe('rendering', () => {
    it('renders button with aria-label=Go back', () => {
      render(<BackButton />);
      expect(screen.getByLabelText('Go back')).toBeInTheDocument();
    });

    it('applies id prop to the button', () => {
      render(<BackButton id='back-nav' />);
      expect(screen.getByTestId('back-btn')).toHaveAttribute('id', 'back-nav');
    });
  });

  describe('navigation', () => {
    it('calls onClick when onClick prop is provided', () => {
      const onClick = jest.fn();
      render(<BackButton onClick={onClick} />);
      fireEvent.click(screen.getByTestId('back-btn'));
      expect(onClick).toHaveBeenCalledTimes(1);
    });

    it('does not call router when onClick is provided', () => {
      const onClick = jest.fn();
      render(<BackButton onClick={onClick} />);
      fireEvent.click(screen.getByTestId('back-btn'));
      expect(mockPush).not.toHaveBeenCalled();
      expect(mockBack).not.toHaveBeenCalled();
    });

    it('calls router.push with backButtonPath when provided', () => {
      render(<BackButton backButtonPath='/dashboard' />);
      fireEvent.click(screen.getByTestId('back-btn'));
      expect(mockPush).toHaveBeenCalledWith('/dashboard');
    });

    it('calls router.back() when history.length > 1 and no path/onClick', () => {
      Object.defineProperty(window, 'history', { value: { length: 3 }, writable: true });
      render(<BackButton />);
      fireEvent.click(screen.getByTestId('back-btn'));
      expect(mockBack).toHaveBeenCalledTimes(1);
    });

    it('calls router.push("/") when history.length <= 1 and no path/onClick', () => {
      Object.defineProperty(window, 'history', { value: { length: 1 }, writable: true });
      render(<BackButton />);
      fireEvent.click(screen.getByTestId('back-btn'));
      expect(mockPush).toHaveBeenCalledWith('/');
    });
  });
});
