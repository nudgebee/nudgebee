import { queryGraphQL } from '@lib/HttpService';
import { loadRpcRoutes } from '@lib/rpcRoutes';
import apiVm from '../index';

jest.mock('@lib/HttpService', () => ({ queryGraphQL: jest.fn(), gqlStringify: jest.fn() }));
jest.mock('@api1/home', () => ({}));
jest.mock('@api1/integrations', () => ({}));
jest.mock('@api1/kubernetes', () => ({}));
jest.mock('@api1/tickets', () => ({}));

// #39859: the single-VM Scan dialog filled its picker from integrations_list,
// which only tenant admins may call, while the scan itself is open to account
// and namespace admins. They got a 403 the dialog showed as "No forager
// discovery datasource is configured for this account".
describe('single-VM scan datasource picker', () => {
  beforeEach(() => jest.clearAllMocks());

  it('reads the datasources through the scan action, not the tenant-admin integrations list', async () => {
    (queryGraphQL as jest.Mock).mockResolvedValue({
      data: { data: { security_list_vm_scan_datasources: { data: [{ id: 'ds-1', name: 'aws-forager-discovery', status: 'enabled' }] } } },
    });

    await expect(apiVm.fetchDiscoveryTargets('account-1')).resolves.toEqual([
      { id: 'ds-1', name: 'aws-forager-discovery', status: 'enabled', host: '', username: '' },
    ]);

    const [query, operationName, variables] = (queryGraphQL as jest.Mock).mock.calls[0];
    expect(query).toContain('security_list_vm_scan_datasources');
    expect(query).not.toContain('integrations_list');
    expect(operationName).toBe('ListVmScanDatasources');
    expect(variables).toEqual({ accountId: 'account-1' });
  });

  it('throws the gateway reason instead of returning an empty list', async () => {
    (queryGraphQL as jest.Mock).mockResolvedValue({
      data: { data: null, errors: [{ message: "Role 'account_admin' is not permitted to invoke 'security_list_vm_scan_datasources'" }] },
    });

    await expect(apiVm.fetchDiscoveryTargets('account-1')).rejects.toThrow('is not permitted to invoke');
  });

  // The root cause was a role mismatch between two actions of one flow. Pin it
  // at the routing table: whoever may start a single-VM scan may load its picker.
  it('admits every role that may start a single-VM scan', () => {
    const routes = loadRpcRoutes();
    const scan = routes['security_scan_vm'];
    const picker = routes['security_list_vm_scan_datasources'];

    expect(scan.allowedRoles.size).toBeGreaterThan(0);
    expect(picker).toBeDefined();
    expect([...scan.allowedRoles].filter((role) => !picker.allowedRoles.has(role))).toEqual([]);
    expect(picker.handler).toBe(scan.handler);
  });
});
