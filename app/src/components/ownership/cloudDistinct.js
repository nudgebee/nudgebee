import { queryGraphQL } from '@lib/HttpService';

const DISTINCT_QUERY = (column) => `
query OwnershipCloudDistinct($where: CloudResourceGroupingsWhereRequest) {
  cloud_resource_groupings_v2(where: $where, column_transformations: [{expr: "distinct", name: "${column}"}]) {
    rows { ${column} }
  }
}`;

// fetchCloudDistinct returns sorted {label,value} options of the distinct values of
// a cloud_resourses column (e.g. "region", "type"), optionally scoped to one cloud
// account. column is a fixed identifier (never user input).
export async function fetchCloudDistinct(column, accountId) {
  const where = accountId ? { account_id: { _eq: accountId } } : {};
  const response = await queryGraphQL(DISTINCT_QUERY(column), 'OwnershipCloudDistinct', { where });
  const rows = response?.data?.data?.cloud_resource_groupings_v2?.rows || [];
  return rows
    .map((r) => r[column])
    .filter(Boolean)
    .sort((a, b) => String(a).localeCompare(String(b)))
    .map((v) => ({ label: v, value: v }));
}
