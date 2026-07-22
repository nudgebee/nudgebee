import * as React from 'react';
import { render, screen } from '@testing-library/react';
import { withAccountGuard } from '@shared/AccountGuard';
import { registerAccountGuard, getAccountGuard, isAccountGuardActive, type AccountGuardImpl } from '@lib/accountGuard';

// The cross-tenant account guard is an ENTERPRISE feature. @shared/AccountGuard
// is the OSS-neutral seam: it must render the page unchanged unless the EE
// bundle has registered an implementation (which OSS builds strip).
//
// The registry (@lib/accountGuard) is module-scoped singleton state, and Jest
// gives each test file its own module registry — so this file starts with NO
// impl registered (the OSS state). We assert the OSS behavior first, then
// register an EE stub and assert delegation. `withAccountGuard` reads the
// registry at render time, so this ordering mirrors the real lifecycle (OSS
// never registers; EE registers once at @ee/init load, before any page render).

const Page = (props: { extra?: string }) => <div data-testid='page'>PAGE:{props.extra ?? ''}</div>;

describe('withAccountGuard — OSS/EE seam', () => {
  describe('OSS build (no EE implementation registered)', () => {
    it('isAccountGuardActive() is false', () => {
      expect(isAccountGuardActive()).toBe(false);
      expect(getAccountGuard()).toBeNull();
    });

    it('renders the wrapped page unchanged (pass-through) — no guard, no banner', () => {
      const Guarded = withAccountGuard(Page, { hideContent: true });
      render(<Guarded extra='oss' />);
      expect(screen.getByTestId('page')).toHaveTextContent('PAGE:oss');
      expect(screen.queryByTestId('ee-guard')).not.toBeInTheDocument();
    });
  });

  describe('EE build (implementation registered)', () => {
    const implSpy = jest.fn();
    const stubImpl: AccountGuardImpl = ({ Component, options, componentProps }) => {
      implSpy({ options, componentProps });
      return (
        <div data-testid='ee-guard' data-hide={String(options.hideContent)}>
          <Component {...componentProps} />
        </div>
      );
    };

    beforeAll(() => registerAccountGuard(stubImpl));

    it('isAccountGuardActive() is true once registered', () => {
      expect(isAccountGuardActive()).toBe(true);
    });

    it('delegates to the EE impl, forwarding Component, options and page props', () => {
      const Guarded = withAccountGuard(Page, { hideContent: true });
      render(<Guarded extra='ee' />);

      // The EE wrapper rendered, received the options...
      expect(screen.getByTestId('ee-guard')).toHaveAttribute('data-hide', 'true');
      expect(implSpy).toHaveBeenCalledWith(
        expect.objectContaining({
          options: expect.objectContaining({ hideContent: true }),
          componentProps: expect.objectContaining({ extra: 'ee' }),
        })
      );
      // ...and the wrapped page still renders through it.
      expect(screen.getByTestId('page')).toHaveTextContent('PAGE:ee');
    });
  });
});
