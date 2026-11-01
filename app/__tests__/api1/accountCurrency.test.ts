import apiCloudAccount from '@api1/cloud-account';
import { queryGraphQL } from '@lib/HttpService';

jest.mock('@lib/HttpService', () => ({
  queryGraphQL: jest.fn(),
  gqlStringify: jest.requireActual('@lib/HttpService').gqlStringify,
}));

const mockQuery = queryGraphQL as jest.Mock;

const currencyResponse = (currency: string | null) => ({
  data: { data: { spend_currency: { rows: currency ? [{ currency_type: currency }] : [] } } },
});

// The cache is module-level, so every test uses its own account id.
describe('apiCloudAccount.getAccountCurrency', () => {
  beforeEach(() => mockQuery.mockReset());
  afterEach(() => jest.useRealTimers());

  it('makes no request for a missing or demo account', async () => {
    await expect(apiCloudAccount.getAccountCurrency('')).resolves.toBeNull();
    await expect(apiCloudAccount.getAccountCurrency('demo')).resolves.toBeNull();
    expect(mockQuery).not.toHaveBeenCalled();
  });

  it('reads the non-aggregate spend_currency_v2 table for the account', async () => {
    mockQuery.mockResolvedValue(currencyResponse('INR'));

    await expect(apiCloudAccount.getAccountCurrency('acc-query')).resolves.toBe('INR');

    const [query, operationName] = mockQuery.mock.calls[0];
    expect(operationName).toBe('accountCurrency');
    expect(query).toContain('spend_currency_v2(');
    expect(query).not.toContain('spend_groupings_v2');
    expect(query).toContain('acc-query');
  });

  it('shares one request between callers that ask at the same time', async () => {
    mockQuery.mockResolvedValue(currencyResponse('USD'));

    const results = await Promise.all([
      apiCloudAccount.getAccountCurrency('acc-shared'),
      apiCloudAccount.getAccountCurrency('acc-shared'),
      apiCloudAccount.getAccountCurrency('acc-shared'),
    ]);

    expect(results).toEqual(['USD', 'USD', 'USD']);
    expect(mockQuery).toHaveBeenCalledTimes(1);
  });

  it('keeps the answer for ten minutes, then asks again', async () => {
    jest.useFakeTimers();
    mockQuery.mockResolvedValue(currencyResponse('USD'));

    await apiCloudAccount.getAccountCurrency('acc-ttl');
    jest.advanceTimersByTime(9 * 60 * 1000);
    await apiCloudAccount.getAccountCurrency('acc-ttl');
    expect(mockQuery).toHaveBeenCalledTimes(1);

    jest.advanceTimersByTime(2 * 60 * 1000);
    await apiCloudAccount.getAccountCurrency('acc-ttl');
    expect(mockQuery).toHaveBeenCalledTimes(2);
  });

  it('keeps "no spend yet" (null) like any other answer', async () => {
    mockQuery.mockResolvedValue(currencyResponse(null));

    await expect(apiCloudAccount.getAccountCurrency('acc-empty')).resolves.toBeNull();
    await expect(apiCloudAccount.getAccountCurrency('acc-empty')).resolves.toBeNull();
    expect(mockQuery).toHaveBeenCalledTimes(1);
  });

  it('does not keep a failed lookup', async () => {
    mockQuery.mockResolvedValueOnce({ data: { errors: [{ message: 'boom' }] } });
    await expect(apiCloudAccount.getAccountCurrency('acc-error')).resolves.toBeNull();

    mockQuery.mockRejectedValueOnce(new Error('network'));
    await expect(apiCloudAccount.getAccountCurrency('acc-error')).resolves.toBeNull();

    mockQuery.mockResolvedValueOnce(currencyResponse('INR'));
    await expect(apiCloudAccount.getAccountCurrency('acc-error')).resolves.toBe('INR');
    expect(mockQuery).toHaveBeenCalledTimes(3);
  });
});
