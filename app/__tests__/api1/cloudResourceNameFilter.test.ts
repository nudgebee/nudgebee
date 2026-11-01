import apiCloudAccount from '@api1/cloud-account';
import { queryGraphQL } from '@lib/HttpService';

jest.mock('@lib/HttpService', () => ({
  queryGraphQL: jest.fn(),
  gqlStringify: jest.requireActual('@lib/HttpService').gqlStringify,
}));

const mockQuery = queryGraphQL as jest.Mock;

const sentQuery = () => mockQuery.mock.calls.at(-1)[0] as string;

// nameFilter is the search box text of every cloud-account listing (EC2, ECS,
// RDS, S3, Cloud Foundry), passed as typed. It becomes a substring match, so a
// space around a pasted name used to be matched literally and find nothing.
describe('apiCloudAccount.getCloudResource nameFilter', () => {
  beforeEach(() => {
    mockQuery.mockReset();
    mockQuery.mockResolvedValue({ data: { data: { cloud_resourses: { rows: [] } } } });
  });

  it('drops spaces around the search text', async () => {
    await apiCloudAccount.getCloudResource({ account_id: 'acc-1', nameFilter: '  nudgebee  ' }, 10, 0);

    expect(mockQuery).toHaveBeenCalledWith(expect.any(String), 'ListCloudResources', { limit: 10, offset: 0 });
    expect(sentQuery()).toContain('"%nudgebee%"');
    expect(sentQuery()).not.toContain('nudgebee ');
    expect(sentQuery()).not.toContain(' nudgebee');
  });

  it('keeps a space inside the search text', async () => {
    await apiCloudAccount.getCloudResource({ account_id: 'acc-1', nameFilter: ' web server ' }, 10, 0);

    expect(sentQuery()).toContain('"%web server%"');
  });

  it('applies no name filter when the search text is only spaces', async () => {
    await apiCloudAccount.getCloudResource({ account_id: 'acc-1', nameFilter: '   ' }, 10, 0);

    expect(sentQuery()).not.toContain('_ilike');
  });
});
