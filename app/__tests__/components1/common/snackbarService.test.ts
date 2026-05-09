import { snackbar } from '@components1/common/snackbarService';

// Reset listeners between tests by re-importing a fresh instance isn't straightforward
// so we use subscribe/unsubscribe to isolate each test
describe('snackbarService', () => {
  test('subscribe registers a callback', () => {
    const callback = jest.fn();
    const unsubscribe = snackbar.subscribe(callback);
    snackbar.show('test', 'success');
    expect(callback).toHaveBeenCalledTimes(1);
    unsubscribe();
  });

  test('success() triggers subscribers with correct message and severity "success"', () => {
    const callback = jest.fn();
    const unsubscribe = snackbar.subscribe(callback);
    snackbar.success('Operation succeeded');
    expect(callback).toHaveBeenCalledWith(expect.objectContaining({ message: 'Operation succeeded', severity: 'success' }));
    unsubscribe();
  });

  test('error() triggers subscribers with correct message and severity "error"', () => {
    const callback = jest.fn();
    const unsubscribe = snackbar.subscribe(callback);
    snackbar.error('Something went wrong');
    expect(callback).toHaveBeenCalledWith(expect.objectContaining({ message: 'Something went wrong', severity: 'error' }));
    unsubscribe();
  });

  test('warning() triggers subscribers with correct message and severity "warning"', () => {
    const callback = jest.fn();
    const unsubscribe = snackbar.subscribe(callback);
    snackbar.warning('Be careful');
    expect(callback).toHaveBeenCalledWith(expect.objectContaining({ message: 'Be careful', severity: 'warning' }));
    unsubscribe();
  });

  test('info() triggers subscribers with correct message and severity "info"', () => {
    const callback = jest.fn();
    const unsubscribe = snackbar.subscribe(callback);
    snackbar.info('FYI: something happened');
    expect(callback).toHaveBeenCalledWith(expect.objectContaining({ message: 'FYI: something happened', severity: 'info' }));
    unsubscribe();
  });

  test('unsubscribe removes the callback (returned function from subscribe)', () => {
    const callback = jest.fn();
    const unsubscribe = snackbar.subscribe(callback);
    unsubscribe();
    snackbar.success('After unsubscribe');
    expect(callback).not.toHaveBeenCalled();
  });
});
