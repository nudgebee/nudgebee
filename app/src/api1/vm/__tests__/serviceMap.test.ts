import { queryGraphQL } from '@lib/HttpService';
import { loadRpcRoutes } from '@lib/rpcRoutes';
import apiVm from '../index';

jest.mock('@lib/HttpService', () => ({ queryGraphQL: jest.fn(), gqlStringify: jest.fn() }));
jest.mock('@api1/home', () => ({}));
jest.mock('@api1/integrations', () => ({}));
jest.mock('@api1/kubernetes', () => ({}));
jest.mock('@api1/tickets', () => ({}));

describe('VM service map', () => {
  beforeEach(() => jest.clearAllMocks());

  it('asks vm_get_service_map for the account and window and returns the applications', async () => {
    const applications = [{ Id: { kind: 'VirtualMachine', name: 'orders-web', namespace: 'vm' }, Upstreams: [], Downstreams: [] }];
    (queryGraphQL as jest.Mock).mockResolvedValue({ data: { data: { vm_get_service_map: { data: { applications } } } } });

    await expect(apiVm.getServiceMap('account-1', 900)).resolves.toEqual(applications);

    const [query, operationName, variables] = (queryGraphQL as jest.Mock).mock.calls[0];
    expect(query).toContain('vm_get_service_map');
    expect(operationName).toBe('VmServiceMap');
    expect(variables).toEqual({ accountId: 'account-1', windowSeconds: 900 });
  });

  it('throws the gateway reason instead of drawing an empty map', async () => {
    (queryGraphQL as jest.Mock).mockResolvedValue({
      data: { data: null, errors: [{ message: "Role 'x' is not permitted to invoke 'vm_get_service_map'" }] },
    });

    await expect(apiVm.getServiceMap('account-1', 900)).rejects.toThrow('is not permitted to invoke');
  });

  // Whoever may open the K8s service map may open this one: same data class, same reader roles.
  it('is routed to the application handler for the same roles as the traces service map', () => {
    const routes = loadRpcRoutes();
    const traces = routes['traces_get_service_map'];
    const vm = routes['vm_get_service_map'];

    expect(vm).toBeDefined();
    expect(vm.handler).toBe(traces.handler);
    expect([...vm.allowedRoles].sort()).toEqual([...traces.allowedRoles].sort());
  });
});
