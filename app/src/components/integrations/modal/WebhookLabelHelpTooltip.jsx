/**
 * WebhookLabelHelpTooltip — info icon answering "which label names can I type here?"
 * for the webhook being edited.
 *
 * Account mapping and webhook label mapping both match on label names, and every
 * provider builds its labels differently, so the list comes from the backend, where
 * it is declared next to that provider's parser (`webhook_label_help` on the
 * integration schema). Nothing here knows about a provider.
 */
import React from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import Tooltip from '@ui/Tooltip';
import SafeIcon from '@shared/icons/SafeIcon';
import { infoIcon } from '@assets';
import { ds } from '@utils/colors';

const TITLE = 'Label names for this webhook';

// The tooltip body sits inside a <p>, so every element here is a span.
const blockSx = { display: 'block' };
const monoSx = { fontFamily: ds.font.mono, fontSize: ds.text.caption, color: ds.gray[700] };

export function WebhookLabelHelpContent({ help, matchesValues = false }) {
  const labels = help?.labels || [];
  const notes = help?.notes || [];
  return (
    <Box component='span' sx={{ ...blockSx, textAlign: 'left' }}>
      {help?.summary && (
        <Box component='span' sx={blockSx}>
          {help.summary}
        </Box>
      )}
      {labels.length > 0 && (
        <Box component='span' sx={{ ...blockSx, mt: ds.space[2] }}>
          <Box component='span' sx={{ ...blockSx, fontWeight: ds.weight.semibold }}>
            Common label names and where each comes from
          </Box>
          {labels.map((row) => (
            <Box component='span' key={row.label} sx={{ ...blockSx, mt: ds.space[1] }} data-testid='webhook-label-help-row'>
              <Box component='span' sx={{ ...monoSx, fontWeight: ds.weight.semibold }}>
                {row.label}
              </Box>
              {' from '}
              <Box component='span' sx={monoSx}>
                {row.source}
              </Box>
            </Box>
          ))}
        </Box>
      )}
      {notes.map((note) => (
        <Box component='span' key={note} sx={{ ...blockSx, mt: ds.space[2] }}>
          {note}
        </Box>
      ))}
      <Box component='span' sx={{ ...blockSx, mt: ds.space[2] }}>
        {matchesValues ? 'Names and values must match exactly, including case.' : 'Label names are case-sensitive.'} A query parameter added to the
        webhook URL, such as{' '}
        <Box component='span' sx={monoSx}>
          &amp;cluster=dev
        </Box>
        , also becomes a label.
      </Box>
    </Box>
  );
}

WebhookLabelHelpContent.propTypes = {
  help: PropTypes.shape({
    summary: PropTypes.string,
    labels: PropTypes.arrayOf(PropTypes.shape({ label: PropTypes.string.isRequired, source: PropTypes.string.isRequired })),
    notes: PropTypes.arrayOf(PropTypes.string),
  }),
  // Account mapping compares label values; webhook label mapping only names keys.
  matchesValues: PropTypes.bool,
};

export default function WebhookLabelHelpTooltip({ help, matchesValues = false }) {
  // An older backend sends no help, and a webhook that produces no labels sends none to list.
  if (!help?.labels?.length) {
    return null;
  }
  return (
    <Tooltip
      variant='explainer'
      title={TITLE}
      desc={<WebhookLabelHelpContent help={help} matchesValues={matchesValues} />}
      maxWidth='360px'
      placement='right'
    >
      <Box component='span' data-testid='webhook-label-help' sx={{ cursor: 'pointer', display: 'inline-flex', alignItems: 'center' }}>
        <SafeIcon src={infoIcon} alt='info' width={14} height={14} />
      </Box>
    </Tooltip>
  );
}

WebhookLabelHelpTooltip.propTypes = {
  help: WebhookLabelHelpContent.propTypes.help,
  matchesValues: PropTypes.bool,
};
