import { Box } from '@mui/material';
import WarningAmberOutlinedIcon from '@mui/icons-material/WarningAmberOutlined';
import PropTypes from 'prop-types';
import Text from '@shared/format/Text';
import Tooltip from '@ui/Tooltip';
import { ds } from 'src/utils/colors';
import { ACCOUNT_ENV_PROD } from '@shared/forms/AccountEnvToggle';

// Matches "prod"/"production" as a word-ish token in an account name (k8s-prod,
// prod-eu, production) without tripping on words like "product".
const PROD_NAME_PATTERN = /(^|[^a-z])prod(uction)?([^a-z]|$)/i;

export const NAME_ENV_MISMATCH_TOOLTIP =
  "This account's name suggests production, but its environment is set to Non-production — the default for accounts that never chose one. Blast-radius production counts, safety bands and alert prioritisation all derive from this setting; edit it from the account's menu.";

/**
 * Environment cell for the accounts tables: renders the account_env tier and
 * flags the likely-stale case — a name that says production while the
 * environment sits on the non_prod column default.
 */
export default function AccountEnvText({ accountName, accountEnv }) {
  const isProd = accountEnv === ACCOUNT_ENV_PROD;
  const nameSuggestsProd = PROD_NAME_PATTERN.test(accountName || '');
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1] }}>
      <Text value={isProd ? 'Production' : 'Non-production'} />
      {!isProd && nameSuggestsProd && (
        <Tooltip title={NAME_ENV_MISMATCH_TOOLTIP} placement='top'>
          <WarningAmberOutlinedIcon data-testid='account-env-mismatch-warning' sx={{ fontSize: '16px', color: ds.amber[600], cursor: 'help' }} />
        </Tooltip>
      )}
    </Box>
  );
}

AccountEnvText.propTypes = {
  accountName: PropTypes.string,
  accountEnv: PropTypes.string,
};
