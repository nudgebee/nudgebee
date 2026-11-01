import { queryGraphQL } from '@lib/HttpService';
import api from '../index';

jest.mock('@lib/HttpService', () => ({ queryGraphQL: jest.fn(), gqlStringify: jest.fn() }));
jest.mock('@api1/integrations', () => ({}));

// The gateway answers a backend failure (for example, an expired client
// secret) with HTTP 200 and an errors array. Discovery must show that reason,
// not report "no subscriptions found".
it('surfaces the backend error instead of an empty subscription list', async () => {
  const log = jest.spyOn(console, 'error').mockImplementation(() => {});
  try {
    (queryGraphQL as jest.Mock).mockResolvedValue({
      data: { data: null, errors: [{ message: 'Invalid client secret provided' }] },
    });
    await expect(api.listAzureSubscriptions('tenant', 'client', 'secret')).rejects.toThrow('Invalid client secret provided');
  } finally {
    log.mockRestore();
  }
});

it('returns the discovered subscriptions', async () => {
  const subscriptions = [{ subscription_id: 'sub-1', display_name: 'Example', state: 'Enabled' }];
  (queryGraphQL as jest.Mock).mockResolvedValue({ data: { data: { azure_list_subscriptions: { subscriptions } } } });
  await expect(api.listAzureSubscriptions('tenant', 'client', 'secret')).resolves.toEqual(subscriptions);
});
