/**
 * The save payload for turning an agent integration's default provider on or off
 * for one cluster. Only that cluster goes in the per-cluster value: the backend
 * records a default the user turned on, so the agent's heartbeat stops taking it
 * back, and re-sending every cluster's value as it was when the modal opened would
 * record choices the user never made. account_ids still lists every linked
 * cluster, because the save unlinks any cluster left out of it.
 */
export const buildAgentProviderTogglePayload = ({ editData, integrationName, accountIds, cloudAccountId, providerKey, newValue }) => ({
  ...(editData?.id && { integration_id: editData.id }),
  integration_name: integrationName,
  account_ids: accountIds,
  integration_config_name: editData?.name,
  skip_validation: true,
  source: editData?.source || 'user',
  integration_config_values: [{ name: providerKey, value: JSON.stringify({ [cloudAccountId]: String(newValue) }), is_encrypted: false }],
});
