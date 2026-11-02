import apiDashboards from '@api1/dashboards';
import { loadVariableValues } from '../panelViewerFilters';

jest.mock('@api1/dashboards', () => ({ __esModule: true, ...jest.requireActual('@api1/dashboards'), default: { executeEntityQuery: jest.fn() } }));

const execute = apiDashboards.executeEntityQuery as jest.Mock;
const HOUR = 60 * 60 * 1000;
const request = { column: 'status', accountIds: ['a', 'b'], startTime: 1000 * HOUR, endTime: 1001 * HOUR };

describe('loadVariableValues', () => {
  beforeEach(() => execute.mockReset());

  it('asks every account for the column over the range, and returns it distinct and sorted', async () => {
    execute.mockResolvedValue({ data: { rows: [['b'], ['a'], ['b'], [null], ['']] } });
    await expect(loadVariableValues({ ...request, table: 'events_v2' })).resolves.toEqual(['a', 'b']);
    const [sent] = execute.mock.calls[0];
    expect(sent).toMatchObject({ account_ids: ['a', 'b'], datasource: 'nudgebee', time_column: 'starts_at', start_time: request.startTime });
    expect(sent.query).toMatchObject({ table: 'events_v2', columns: [{ name: 'status' }] });
  });

  it('says why when the engine refuses', async () => {
    execute.mockResolvedValue({ data: null, errors: [{ message: 'not allowed' }] });
    await expect(loadVariableValues({ ...request, table: 'events_v2' })).rejects.toThrow('not allowed');
  });
});
