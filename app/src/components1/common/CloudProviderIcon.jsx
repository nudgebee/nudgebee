import {
  cloudBlackIcon,
  ouAws,
  ouAzure,
  AWSIcon,
  ouGoogle,
  ouK8s,
  ouSnowFlake,
  ouOpenAi,
  ouRelic,
  jiraIcon as JiraIcon,
  slackIcon as SlackIcon,
  SplunkIcon,
} from '@assets';

import { Box } from '@mui/material';
import PropTypes from 'prop-types';

const resolveIcon = (icon) => {
  if (typeof icon === 'function' || (typeof icon === 'object' && icon?.$$typeof)) return { src: icon, isComponent: true };
  if (icon?.default?.src) return { src: icon.default.src, isComponent: false };
  if (typeof icon === 'string') return { src: icon, isComponent: false };
  return null;
};

const CloudProviderIcon = ({ cloud_provider, height = '28px', width = '28px', sx = {} }) => {
  let resolved = null;

  const provider = cloud_provider?.toUpperCase();

  if (provider === 'AWS') {
    resolved = resolveIcon(AWSIcon) || resolveIcon(ouAws);
  } else if (provider === 'GCP') {
    resolved = resolveIcon(ouGoogle);
  } else if (provider === 'AZURE') {
    resolved = resolveIcon(ouAzure);
  } else if (provider === 'K8S') {
    resolved = resolveIcon(ouK8s);
  } else if (provider === 'SNOWFLAKE') {
    resolved = resolveIcon(ouSnowFlake);
  } else if (provider === 'OPENAI') {
    resolved = resolveIcon(ouOpenAi);
  } else if (provider === 'NEWRELIC') {
    resolved = resolveIcon(ouRelic);
  } else if (provider === 'JIRA') {
    resolved = resolveIcon(JiraIcon);
  } else if (provider === 'SLACK') {
    resolved = resolveIcon(SlackIcon);
  } else if (provider === 'SPLUNK_WEBHOOK' || provider === 'SPLUNK_OBSERVABILITY_PLATFORM') {
    resolved = resolveIcon(SplunkIcon);
  }

  if (!resolved) {
    resolved = resolveIcon(ouAws) || resolveIcon(cloudBlackIcon);
  }

  const { src: Icon, isComponent } = resolved;

  if (isComponent) {
    return <Box component={Icon} sx={{ height, width, ...sx }} />;
  }

  return (
    <Box
      component='img'
      sx={{
        height,
        width,
        ...sx,
      }}
      alt='aws'
      src={Icon}
    />
  );
};

CloudProviderIcon.propTypes = {
  cloud_provider: PropTypes.string.isRequired,
  height: PropTypes.string,
  width: PropTypes.string,
  sx: PropTypes.object,
};

export default CloudProviderIcon;
