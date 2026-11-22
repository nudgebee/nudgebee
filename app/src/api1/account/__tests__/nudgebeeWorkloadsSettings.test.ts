import { queryGraphQL } from '@lib/HttpService';
import { buildNudgebeeWorkloadsValue, getAgentInstallNamespace, parseNudgebeeWorkloadsForm } from '../nudgebeeWorkloadsSettings';

jest.mock('@lib/HttpService', () => ({
  queryGraphQL: jest.fn(),
  gqlStringify: jest.requireActual('@lib/HttpService').gqlStringify,
}));

const query = queryGraphQL as jest.Mock;

describe('nudgebee_workloads setting', () => {
  it('hides by default when the account has no setting', () => {
    expect(parseNudgebeeWorkloadsForm(undefined)).toEqual({ hideInNotifications: true, namespaces: '' });
    expect(parseNudgebeeWorkloadsForm('not json')).toEqual({ hideInNotifications: true, namespaces: '' });
  });

  it('round-trips a stored value', () => {
    const form = parseNudgebeeWorkloadsForm('{"hide_in_notifications":false,"namespaces":["nudgebee","acme-nudgebee"]}');
    expect(form).toEqual({ hideInNotifications: false, namespaces: 'nudgebee, acme-nudgebee' });
    expect(buildNudgebeeWorkloadsValue(form)).toEqual({ value: '{"hide_in_notifications":false,"namespaces":["nudgebee","acme-nudgebee"]}' });
  });

  it('rejects a name that is not a Kubernetes namespace', () => {
    expect(buildNudgebeeWorkloadsValue({ hideInNotifications: true, namespaces: 'nudgebee, Bad_Name' })).toEqual({
      error: '"Bad_Name" is not a valid Kubernetes namespace',
    });
  });

  it('reads the namespace the agent reports, as an object or a JSON string', async () => {
    query.mockResolvedValueOnce({ data: { data: { agent: { rows: [{ connection_status: { installationNamespace: 'acme-observability' } }] } } } });
    await expect(getAgentInstallNamespace('account-1')).resolves.toBe('acme-observability');

    query.mockResolvedValueOnce({ data: { data: { agent: { rows: [{ connection_status: '{"installationNamespace":"nudgebee-agent"}' }] } } } });
    await expect(getAgentInstallNamespace('account-1')).resolves.toBe('nudgebee-agent');

    query.mockResolvedValueOnce({ data: { data: { agent: { rows: [{ connection_status: null }] } } } });
    await expect(getAgentInstallNamespace('account-1')).resolves.toBe('');
  });
});
