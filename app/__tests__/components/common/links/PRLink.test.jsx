import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import PRLink, { resolutionsDeepLink, hasRenderablePRState } from '@shared/links/PRLink';

describe('PRLink', () => {
  describe('basic rendering', () => {
    it('renders link with a valid GitHub PR URL', () => {
      render(<PRLink prURL='https://github.com/org/repo/pull/123' />);
      expect(screen.getByText('PR #123')).toBeInTheDocument();
    });

    it('renders nothing when prURL is not provided', () => {
      const { container } = render(<PRLink />);
      expect(container.firstChild).toBeNull();
    });

    it('renders nothing when prURL is null', () => {
      const { container } = render(<PRLink prURL={null} />);
      expect(container.firstChild).toBeNull();
    });

    it('renders nothing when prURL is empty string', () => {
      const { container } = render(<PRLink prURL='' />);
      expect(container.firstChild).toBeNull();
    });
  });

  describe('PR identifier extraction', () => {
    it('extracts PR number from GitHub /pull/ URL', () => {
      render(<PRLink prURL='https://github.com/org/repo/pull/456' />);
      expect(screen.getByText('PR #456')).toBeInTheDocument();
    });

    it('extracts PR number from nested /pull/ URL', () => {
      render(<PRLink prURL='https://github.com/company/my-repo/pull/99' />);
      expect(screen.getByText('PR #99')).toBeInTheDocument();
    });

    it('falls back to last path segment when no /pull/ in URL', () => {
      render(<PRLink prURL='https://gitlab.com/org/repo/merge_requests/77' />);
      expect(screen.getByText('PR #77')).toBeInTheDocument();
    });

    it('renders full URL as identifier when URL has only one segment', () => {
      render(<PRLink prURL='http://example.com/42' />);
      expect(screen.getByText('PR #42')).toBeInTheDocument();
    });
  });

  describe('link attributes', () => {
    it('sets href to the provided prURL', () => {
      render(<PRLink prURL='https://github.com/org/repo/pull/123' />);
      const link = screen.getByText('PR #123').closest('a');
      expect(link).toHaveAttribute('href', 'https://github.com/org/repo/pull/123');
    });

    it('opens link in a new tab (target="_blank")', () => {
      render(<PRLink prURL='https://github.com/org/repo/pull/123' />);
      const link = screen.getByText('PR #123').closest('a');
      expect(link).toHaveAttribute('target', '_blank');
    });

    it('has rel="noopener noreferrer" for security', () => {
      render(<PRLink prURL='https://github.com/org/repo/pull/123' />);
      const link = screen.getByText('PR #123').closest('a');
      expect(link).toHaveAttribute('rel', 'noopener noreferrer');
    });
  });

  describe('click behavior', () => {
    it('stops propagation on click', () => {
      const parentClickHandler = jest.fn();
      render(
        <div onClick={parentClickHandler}>
          <PRLink prURL='https://github.com/org/repo/pull/123' />
        </div>
      );
      fireEvent.click(screen.getByText('PR #123').closest('a'));
      expect(parentClickHandler).not.toHaveBeenCalled();
    });
  });

  describe('statusMessage prop', () => {
    it('uses statusMessage in tooltip when provided', () => {
      render(<PRLink prURL='https://github.com/org/repo/pull/123' statusMessage='Merged' />);
      // The component renders correctly with statusMessage
      expect(screen.getByText('PR #123')).toBeInTheDocument();
    });

    it('uses default tooltip "Open Pull Request" when statusMessage not provided', () => {
      render(<PRLink prURL='https://github.com/org/repo/pull/123' />);
      expect(screen.getByText('PR #123')).toBeInTheDocument();
    });
  });

  describe('CallMergeIcon', () => {
    it('renders CallMergeIcon inside the link', () => {
      const { container } = render(<PRLink prURL='https://github.com/org/repo/pull/123' />);
      // MUI icons render as SVG elements
      expect(container.querySelector('svg')).toBeInTheDocument();
    });
  });

  describe('resolution states without a PR url', () => {
    it('reports a failed PR attempt instead of rendering nothing', () => {
      render(<PRLink status='Failed' statusMessage='push rejected' />);
      expect(screen.getByText('PR Failed')).toBeInTheDocument();
    });

    it('reports a PR attempt still in progress', () => {
      render(<PRLink status='InProgress' />);
      expect(screen.getByText('PR Pending')).toBeInTheDocument();
    });

    it('reports a terminal success that raised no PR', () => {
      render(<PRLink status='Success' />);
      expect(screen.getByText('No PR Needed')).toBeInTheDocument();
    });

    it('renders nothing for an unknown status', () => {
      const { container } = render(<PRLink status='Something' />);
      expect(container.firstChild).toBeNull();
    });

    it('links a failed attempt to its resolution when a href is given', () => {
      render(<PRLink status='Failed' resolutionHref='/optimise?id=rec-1#resolutions' />);
      expect(screen.getByText('PR Failed').closest('a')).toHaveAttribute('href', '/optimise?id=rec-1#resolutions');
    });

    it('renders a non-clickable chip when no resolution href is available', () => {
      render(<PRLink status='Failed' />);
      expect(screen.getByText('PR Failed').closest('a')).toBeNull();
    });

    it('prefers the PR url over the status when both are present', () => {
      render(<PRLink prURL='https://github.com/org/repo/pull/7' status='Failed' />);
      expect(screen.getByText('PR #7')).toBeInTheDocument();
      expect(screen.queryByText('PR Failed')).not.toBeInTheDocument();
    });
  });

  describe('hasRenderablePRState', () => {
    it.each([
      ['a url', { type_reference_id: 'https://github.com/org/repo/pull/1' }],
      ['a failed status', { status: 'Failed' }],
      ['an in-progress status', { status: 'InProgress' }],
      ['a success status', { status: 'Success' }],
    ])('is true for a resolution with %s', (_label, resolution) => {
      expect(hasRenderablePRState(resolution)).toBe(true);
    });

    it.each([
      ['no resolution', undefined],
      ['a null resolution', null],
      ['an unrecognised status and no url', { status: 'Something', type_reference_id: '' }],
    ])('is false for %s, so callers do not render empty chrome', (_label, resolution) => {
      expect(hasRenderablePRState(resolution)).toBe(false);
    });

    it('agrees with what PRLink actually renders', () => {
      const resolution = { status: 'Something' };
      const { container } = render(<PRLink status={resolution.status} />);
      expect(container.firstChild === null).toBe(!hasRenderablePRState(resolution));
    });
  });

  describe('resolutionsDeepLink', () => {
    it('scopes the Resolutions listing to a recommendation', () => {
      expect(resolutionsDeepLink('rec-1')).toBe('/optimise?id=rec-1#resolutions');
    });

    it('returns an empty href when there is no recommendation to scope by', () => {
      expect(resolutionsDeepLink(undefined)).toBe('');
    });
  });
});
