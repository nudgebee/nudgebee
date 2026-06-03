import React from 'react';
import { Box, Tooltip, Typography } from '@mui/material';
import HistoryIcon from '@mui/icons-material/History';
import { Chip } from '@components1/ds/Chip';
import CustomButton from '@common/NewCustomButton';
import { colors } from 'src/utils/colors';

interface WorkflowStateStripProps {
  /** Canvas has edits not yet persisted to the saved draft. */
  hasUnsavedChanges?: boolean;
  /** Saved draft has been changed since the live version was published (saved but not published). */
  draftAheadOfLive?: boolean;
  liveVersionNumber?: number | null;
  liveVersionName?: string | null;
  liveVersionId?: string | null;
  /** Hide Publish/History + live indicator for never-saved new workflows. */
  isNewWorkflow?: boolean;
  onPublish?: () => void;
  onHistory?: () => void;
}

/**
 * WorkflowStateStrip — the always-visible answer to "what am I looking at, and
 * what actually runs?". Sits in the builder header's top-right. Surfaces the
 * three definition layers (canvas draft → saved draft → live version) and hosts
 * the Publish + History actions. Enablement status lives in the bottom action
 * bar, not here.
 */
const WorkflowStateStrip: React.FC<WorkflowStateStripProps> = ({
  hasUnsavedChanges = false,
  draftAheadOfLive = false,
  liveVersionNumber,
  liveVersionName,
  liveVersionId,
  isNewWorkflow = false,
  onPublish,
  onHistory,
}) => {
  const hasLiveVersion = Boolean(liveVersionId);
  // Saved, but the saved draft is ahead of the published live version.
  const aheadOfLive = !hasUnsavedChanges && draftAheadOfLive && hasLiveVersion;
  // On-screen draft matches the live version (nothing pending). Only then do we
  // show the "Live vN" chip — when ahead/unsaved the draft label already carries
  // the version, so the extra chip would be redundant.
  const inSyncWithLive = hasLiveVersion && !hasUnsavedChanges && !aheadOfLive;

  const draftLabel = hasUnsavedChanges ? 'Unsaved changes' : aheadOfLive ? `Draft saved (ahead of Live v${liveVersionNumber ?? '?'})` : 'Draft saved';

  const draftTooltip = hasUnsavedChanges
    ? 'You have edits on the canvas that are not saved yet. Click Save draft to persist them.'
    : aheadOfLive
    ? `Your saved draft has changes that aren't live yet. Scheduled and event triggers still run Live v${
        liveVersionNumber ?? '?'
      }. Click Publish to make the draft live.`
    : 'The canvas matches the saved draft.';

  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }} data-testid='workflow-state-strip'>
      {/* Draft layer: canvas vs saved draft vs live */}
      {!isNewWorkflow && (
        <Tooltip title={draftTooltip}>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }} data-testid='workflow-draft-indicator'>
            <Box
              sx={{
                width: 8,
                height: 8,
                borderRadius: '50%',
                backgroundColor: hasUnsavedChanges || aheadOfLive ? '#f59e0b' : colors.text.tertiarymedium,
              }}
            />
            <Typography sx={{ fontSize: '12px', fontWeight: 500, color: colors.text.secondary }}>{draftLabel}</Typography>
          </Box>
        </Tooltip>
      )}

      {/* Live version chip — only when the draft is in sync with live (showing
          both "ahead of Live vN" text AND a "Live vN" chip is redundant), or
          when no live version exists yet. */}
      {!isNewWorkflow && inSyncWithLive && (
        <Tooltip title={`Scheduled and event triggers run the live version${liveVersionName ? ` (“${liveVersionName}”)` : ''}.`}>
          <Box sx={{ display: 'flex', alignItems: 'center' }} data-testid='workflow-live-indicator'>
            <Chip size='xs' variant='tag' tone='info'>
              {`Live v${liveVersionNumber ?? '?'}`}
            </Chip>
          </Box>
        </Tooltip>
      )}
      {!isNewWorkflow && !hasLiveVersion && (
        <Tooltip title='No live version yet. Publish to create one — scheduled and event triggers run the live version.'>
          <Box sx={{ display: 'flex', alignItems: 'center' }} data-testid='workflow-live-indicator'>
            <Chip size='xs' variant='tag' tone='neutral'>
              No live version
            </Chip>
          </Box>
        </Tooltip>
      )}

      {/* Actions: History + Publish (moved here from the bottom action bar) */}
      {!isNewWorkflow && (
        <>
          {onPublish && (
            <CustomButton
              id='workflow-publish-btn'
              data-testid='workflow-publish-btn'
              onClick={onPublish}
              text='Publish'
              variant='tertiary'
              size='Small'
            />
          )}
          {onHistory && (
            <CustomButton
              id='workflow-history-btn'
              data-testid='workflow-history-btn'
              onClick={onHistory}
              text='History'
              variant='secondary'
              size='Small'
              startIcon={<HistoryIcon sx={{ fontSize: 16 }} />}
            />
          )}
        </>
      )}
    </Box>
  );
};

export default WorkflowStateStrip;
