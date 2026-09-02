import { snackbar, type SnackbarOptions } from '@shared/snackbarService';

describe('snackbarService', () => {
  describe('subscribe / unsubscribe', () => {
    it('calls subscriber when show is called', () => {
      const listener = jest.fn();
      const unsub = snackbar.subscribe(listener);
      snackbar.show('hello', 'success');
      expect(listener).toHaveBeenCalledTimes(1);
      expect(listener).toHaveBeenCalledWith(expect.objectContaining({ message: 'hello', severity: 'success' }));
      unsub();
    });

    it('stops calling subscriber after unsubscribe', () => {
      const listener = jest.fn();
      const unsub = snackbar.subscribe(listener);
      unsub();
      snackbar.show('after unsub', 'info');
      expect(listener).not.toHaveBeenCalled();
    });

    it('supports multiple subscribers', () => {
      const a = jest.fn();
      const b = jest.fn();
      const ua = snackbar.subscribe(a);
      const ub = snackbar.subscribe(b);
      snackbar.show('msg', 'warning');
      expect(a).toHaveBeenCalledTimes(1);
      expect(b).toHaveBeenCalledTimes(1);
      ua();
      ub();
    });

    it('unsubscribing one listener does not affect others', () => {
      const a = jest.fn();
      const b = jest.fn();
      const ua = snackbar.subscribe(a);
      const ub = snackbar.subscribe(b);
      ua();
      snackbar.show('only b', 'success');
      expect(a).not.toHaveBeenCalled();
      expect(b).toHaveBeenCalledTimes(1);
      ub();
    });
  });

  describe('convenience methods', () => {
    let listener: jest.Mock;
    let unsub: () => void;

    beforeEach(() => {
      listener = jest.fn();
      unsub = snackbar.subscribe(listener);
    });

    afterEach(() => {
      unsub();
    });

    it('success() calls show with severity=success', () => {
      snackbar.success('done');
      const opts: SnackbarOptions = listener.mock.calls[0][0];
      expect(opts.severity).toBe('success');
      expect(opts.message).toBe('done');
    });

    it('error() calls show with severity=error', () => {
      snackbar.error('oops');
      const opts: SnackbarOptions = listener.mock.calls[0][0];
      expect(opts.severity).toBe('error');
      expect(opts.message).toBe('oops');
    });

    it('warning() calls show with severity=warning', () => {
      snackbar.warning('watch out');
      const opts: SnackbarOptions = listener.mock.calls[0][0];
      expect(opts.severity).toBe('warning');
    });

    it('info() calls show with severity=info', () => {
      snackbar.info('fyi');
      const opts: SnackbarOptions = listener.mock.calls[0][0];
      expect(opts.severity).toBe('info');
    });

    it('forwards optional duration', () => {
      snackbar.show('msg', 'success', 5000);
      const opts: SnackbarOptions = listener.mock.calls[0][0];
      expect(opts.duration).toBe(5000);
    });

    it('duration is undefined when not provided', () => {
      snackbar.show('msg', 'info');
      const opts: SnackbarOptions = listener.mock.calls[0][0];
      expect(opts.duration).toBeUndefined();
    });
  });
});
