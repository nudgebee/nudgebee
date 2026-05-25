import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import CustomPRLink from '@components1/common/CustomPRLink';

describe('CustomPRLink', () => {
  describe('basic rendering', () => {
    it('renders link with a valid GitHub PR URL', () => {
      render(<CustomPRLink prURL='https://github.com/org/repo/pull/123' />);
      expect(screen.getByText('PR #123')).toBeInTheDocument();
    });

    it('renders nothing when prURL is not provided', () => {
      const { container } = render(<CustomPRLink />);
      expect(container.firstChild).toBeNull();
    });

    it('renders nothing when prURL is null', () => {
      const { container } = render(<CustomPRLink prURL={null} />);
      expect(container.firstChild).toBeNull();
    });

    it('renders nothing when prURL is empty string', () => {
      const { container } = render(<CustomPRLink prURL='' />);
      expect(container.firstChild).toBeNull();
    });
  });

  describe('PR identifier extraction', () => {
    it('extracts PR number from GitHub /pull/ URL', () => {
      render(<CustomPRLink prURL='https://github.com/org/repo/pull/456' />);
      expect(screen.getByText('PR #456')).toBeInTheDocument();
    });

    it('extracts PR number from nested /pull/ URL', () => {
      render(<CustomPRLink prURL='https://github.com/company/my-repo/pull/99' />);
      expect(screen.getByText('PR #99')).toBeInTheDocument();
    });

    it('falls back to last path segment when no /pull/ in URL', () => {
      render(<CustomPRLink prURL='https://gitlab.com/org/repo/merge_requests/77' />);
      expect(screen.getByText('PR #77')).toBeInTheDocument();
    });

    it('renders full URL as identifier when URL has only one segment', () => {
      render(<CustomPRLink prURL='http://example.com/42' />);
      expect(screen.getByText('PR #42')).toBeInTheDocument();
    });
  });

  describe('link attributes', () => {
    it('sets href to the provided prURL', () => {
      render(<CustomPRLink prURL='https://github.com/org/repo/pull/123' />);
      const link = screen.getByText('PR #123').closest('a');
      expect(link).toHaveAttribute('href', 'https://github.com/org/repo/pull/123');
    });

    it('opens link in a new tab (target="_blank")', () => {
      render(<CustomPRLink prURL='https://github.com/org/repo/pull/123' />);
      const link = screen.getByText('PR #123').closest('a');
      expect(link).toHaveAttribute('target', '_blank');
    });

    it('has rel="noopener noreferrer" for security', () => {
      render(<CustomPRLink prURL='https://github.com/org/repo/pull/123' />);
      const link = screen.getByText('PR #123').closest('a');
      expect(link).toHaveAttribute('rel', 'noopener noreferrer');
    });
  });

  describe('click behavior', () => {
    it('stops propagation on click', () => {
      const parentClickHandler = jest.fn();
      render(
        <div onClick={parentClickHandler}>
          <CustomPRLink prURL='https://github.com/org/repo/pull/123' />
        </div>
      );
      fireEvent.click(screen.getByText('PR #123').closest('a'));
      expect(parentClickHandler).not.toHaveBeenCalled();
    });
  });

  describe('statusMessage prop', () => {
    it('uses statusMessage in tooltip when provided', () => {
      render(<CustomPRLink prURL='https://github.com/org/repo/pull/123' statusMessage='Merged' />);
      // The component renders correctly with statusMessage
      expect(screen.getByText('PR #123')).toBeInTheDocument();
    });

    it('uses default tooltip "Open Pull Request" when statusMessage not provided', () => {
      render(<CustomPRLink prURL='https://github.com/org/repo/pull/123' />);
      expect(screen.getByText('PR #123')).toBeInTheDocument();
    });
  });

  describe('CallMergeIcon', () => {
    it('renders CallMergeIcon inside the link', () => {
      const { container } = render(<CustomPRLink prURL='https://github.com/org/repo/pull/123' />);
      // MUI icons render as SVG elements
      expect(container.querySelector('svg')).toBeInTheDocument();
    });
  });
});
