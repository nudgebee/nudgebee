import { queryGraphQL } from '@lib/HttpService';
import api from '@api1/account';

jest.mock('@lib/HttpService', () => ({ queryGraphQL: jest.fn(), gqlStringify: jest.fn() }));
jest.mock('@api1/integrations', () => ({}));

describe('getNotificationUserList', () => {
  it('throws the gateway error instead of returning an empty list', async () => {
    (queryGraphQL as jest.Mock).mockResolvedValue({
      data: { data: { notification_get_user_list: null }, errors: [{ message: 'Rate limit exceeded. Please retry shortly.' }] },
    });
    await expect(api.getNotificationUserList('slack')).rejects.toThrow('Rate limit exceeded. Please retry shortly.');
  });

  it('throws when the list comes back without a users array', async () => {
    (queryGraphQL as jest.Mock).mockResolvedValue({ data: { data: { notification_get_user_list: {} } } });
    await expect(api.getNotificationUserList('slack')).rejects.toThrow('User list response had no users array');
  });

  it('returns the users', async () => {
    const users = [{ id: 'U1', display_name: 'Alice' }];
    (queryGraphQL as jest.Mock).mockResolvedValue({ data: { data: { notification_get_user_list: { data: users } } } });
    await expect(api.getNotificationUserList('slack')).resolves.toEqual({ data: { data: users } });
  });
});
