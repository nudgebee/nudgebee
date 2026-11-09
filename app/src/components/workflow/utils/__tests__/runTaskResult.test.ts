import { toRunTaskOutcome } from '../runTaskResult';

describe('toRunTaskOutcome', () => {
  it('keeps a completed run COMPLETED when the task output carries its own error, and passes that error on', () => {
    // network.tcp against an unreachable host: the run completes, the output says why.
    const result = { error: 'dial tcp 192.0.2.1:443: i/o timeout', host: 'example.com', port: '443', reachable: false, response: '' };

    expect(toRunTaskOutcome({ status: 'COMPLETED', result })).toEqual({
      status: 'COMPLETED',
      output: result,
      error: 'dial tcp 192.0.2.1:443: i/o timeout',
    });
  });

  it('adds no error when the task output reports an empty one', () => {
    const result = { error: '', host: 'example.com', port: '443', reachable: true, response: '' };

    expect(toRunTaskOutcome({ status: 'COMPLETED', result })).toEqual({ status: 'COMPLETED', output: result });
  });

  it('hands over the whole output when the task output has an output field of its own', () => {
    // network.ping returns the raw ping text under `output` next to the parsed fields.
    const result = { host: '192.0.2.1', reachable: true, packet_loss: 0, output: '3 packets transmitted' };

    expect(toRunTaskOutcome({ status: 'COMPLETED', result })).toEqual({ status: 'COMPLETED', output: result });
  });

  it('reports a failed run with the error the API returned', () => {
    expect(toRunTaskOutcome({ status: 'FAILED', error: 'invalid port "443x": use a port number between 1 and 65535', result: null })).toEqual({
      error: 'invalid port "443x": use a port number between 1 and 65535',
    });
  });

  it('falls back to a generic message when a failed run carries no error', () => {
    expect(toRunTaskOutcome({ status: 'FAILED' })).toEqual({ error: 'Task execution failed' });
  });

  it('treats a missing response as a completed run with no output', () => {
    expect(toRunTaskOutcome(undefined)).toEqual({ status: 'COMPLETED', output: null });
  });
});
