import { queryGraphQL } from '@lib/HttpService';
import { getKnowledgePolicy, saveKnowledgePolicy } from '../policy';
jest.mock('@lib/HttpService', () => ({ queryGraphQL: jest.fn() }));
const query = queryGraphQL as jest.Mock;
beforeEach(() => query.mockReset());
it('defaults only a successful missing-value response to auto', async () => {
  query.mockResolvedValue({ data: { data: { cloud_account_attrs_v2: { rows: [] } } } });
  expect(await getKnowledgePolicy('account-a')).toBe('auto');
  expect(query.mock.calls[0][2]).toEqual({ accountId: 'account-a' });
  query.mockResolvedValue({ data: { errors: [{ message: 'denied' }] } });
  await expect(getKnowledgePolicy('account-a')).rejects.toThrow('Could not load');
});
it('rejects unknown stored modes instead of silently showing auto', async () => {
  query.mockResolvedValue({ data: { data: { cloud_account_attrs_v2: { rows: [{ value: 'invalid' }] } } } });
  await expect(getKnowledgePolicy('account-a')).rejects.toThrow('invalid');
});
it('writes only the selected account policy and requires confirmed success', async () => {
  query.mockResolvedValue({ data: { data: { cloud_account_attrs_upsert: { affected_rows: 1 } } } });
  await saveKnowledgePolicy('account-a', 'disabled');
  expect(query.mock.calls[0][2]).toEqual({ object: { objects: [{ cloud_account_id: 'account-a', name: 'knowledge_policy', value: 'disabled' }] } });
  query.mockResolvedValue({ data: { data: { cloud_account_attrs_upsert: { affected_rows: 0 } } } });
  await expect(saveKnowledgePolicy('account-a', 'auto')).rejects.toThrow('Could not save');
});
