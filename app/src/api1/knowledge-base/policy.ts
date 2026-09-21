import { queryGraphQL } from '@lib/HttpService';
import { INSERT_ACC_ATTRIBUTE } from '@api1/account';

export const KNOWLEDGE_POLICIES = ['auto', 'always', 'llm_only', 'disabled'] as const;
export type KnowledgePolicy = (typeof KNOWLEDGE_POLICIES)[number];

const GET_POLICY = `query KnowledgePolicy($accountId: String!) {
  cloud_account_attrs_v2(where: {cloud_account_id: {_eq: $accountId}, name: {_eq: "knowledge_policy"}}) {
    rows { value }
  }
}`;

export async function getKnowledgePolicy(accountId: string): Promise<KnowledgePolicy> {
  const response = await queryGraphQL(GET_POLICY, 'KnowledgePolicy', { accountId });
  const rows = response?.data?.data?.cloud_account_attrs_v2?.rows;
  if (response?.data?.errors?.length || !Array.isArray(rows)) {
    throw new Error('Could not load the knowledge policy. Retry before making changes.');
  }
  const rawValue = rows[0]?.value;
  if (rawValue != null && typeof rawValue !== 'string') {
    throw new Error('The saved knowledge policy is invalid. Contact your administrator.');
  }
  const value = (rawValue ?? '').trim().toLowerCase() || 'auto';
  if (!KNOWLEDGE_POLICIES.includes(value)) throw new Error('The saved knowledge policy is invalid. Contact your administrator.');
  return value;
}

export async function saveKnowledgePolicy(accountId: string, value: KnowledgePolicy): Promise<void> {
  if (!KNOWLEDGE_POLICIES.includes(value)) throw new Error('Select a valid knowledge policy.');
  const response = await queryGraphQL(INSERT_ACC_ATTRIBUTE, 'InsertAccountAttribute', {
    object: { objects: [{ cloud_account_id: accountId, name: 'knowledge_policy', value }] },
  });
  if (response?.data?.errors?.length || response?.data?.data?.cloud_account_attrs_upsert?.affected_rows !== 1) {
    throw new Error('Could not save the knowledge policy. Your change has not been confirmed. Please retry.');
  }
}
