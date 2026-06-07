import React from 'react';
import { Box, Typography } from '@mui/material';
import HistoryIcon from '@mui/icons-material/History';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import Tooltip from '@components1/ds/Tooltip';
import { Chip } from '@components1/ds/Chip';
import { Button } from '@components1/ds/Button';
import { ds } from 'src/utils/colors';

interface WorkflowStateStripProps {
  /** Canvas has edits not yet persisted to the saved draft. */
  hasUnsavedChanges?: boolean;
  /**
   * Draft has been edited since the last publish (saved or unsaved). Independent
   * of hasUnsavedChanges so the "unpublished changes" warning persists after the
   * user clicks Save Draft. Owned by WorkflowBuilderNotebook (session-scoped flag
   * that flips true on first edit and resets on a successful publish or reload).
   */
  draftAheadOfLive?: boolean;
  liveVersionNumber?: number | null;
  liveVersionName?: string | null;
  liveVersionId?: string | null;
  /**
   * The workflow_versions row the current draft (workflows.definition) was
   * last branched off — set on create, publish, and restore. When this differs
   * from the live version the user is "checked out" on an older version, and
   * the strip renders a `Draft based on vN` chip alongside `Live vM`.
   */
  draftVersionNumber?: number | null;
  draftVersionName?: string | null;
  draftVersionId?: string | null;
  /** Hide Publish/History + live indicator for never-saved new workflows. */
  isNewWorkflow?: boolean;
  onPublish?: () => void;
  onHistory?: () => void;
}

/**
 * WorkflowStateStrip — the always-visible answer to "what am I looking at, and
 * what actually runs?". Sits in the builder header's top-right.
 *
 * Layout: `[Draft chip?] [Live vN chip] [Info ⓘ] [Publish] [History]`.
 *
 * The legacy "ahead of Live vX" hint was a definition-hash diff with no
 * lineage — it lied whenever the user restored an older version (it said
 * "ahead of Live v3" even when the draft was branched off v1). It is replaced
 * by an explicit `Draft based on vN` chip driven by workflows.draft_version_id,
 * so the lineage is always accurate. A small unsaved-edits dot still appears
 * when the canvas hasn't been persisted to the saved draft yet.
 */
const WorkflowStateStrip: React.FC<WorkflowStateStripProps> = ({
  hasUnsavedChanges = false,
  draftAheadOfLive = false,
  liveVersionNumber,
  liveVersionName,
  liveVersionId,
  draftVersionNumber,
  draftVersionName,
  draftVersionId,
  isNewWorkflow = false,
  onPublish,
  onHistory,
}) => {
  const hasLiveVersion = Boolean(liveVersionId);
  // A "checkout" is any time the draft is branched off a version that is NOT
  // the live one — typically after a restore from history. Brand-new workflows
  // have draft == live so this is false.
  const isCheckedOut = Boolean(draftVersionId) && Boolean(liveVersionId) && draftVersionId !== liveVersionId;
  // "Unpublished" tracks both saved + unsaved edits. hasUnsavedChanges flips
  // back to false once the user clicks Save Draft, but the draft is still
  // unpublished against the live version, so the chip must persist.
  // draftAheadOfLive (session-scoped, owned by WorkflowBuilderNotebook) keeps
  // the warning visible across the save/publish boundary.
  const hasUnpublished = hasUnsavedChanges || draftAheadOfLive;
  const draftChipLabel = isCheckedOut
    ? `Draft based on v${draftVersionNumber ?? '?'}${hasUnpublished ? ' + unpublished changes' : ''}`
    : hasUnpublished
    ? 'Draft + unpublished changes'
    : null;
  const draftChipTooltip = isCheckedOut
    ? `Your draft is based on v${draftVersionNumber ?? '?'}${draftVersionName ? ` (“${draftVersionName}”)` : ''}. All triggers run Live version: v${
        liveVersionNumber ?? '?'
      }. Publish to make your draft live.`
    : hasUnpublished
    ? `Your draft is saved but not published. All triggers run Live version: v${liveVersionNumber ?? '?'}. Publish to make your draft live.`
    : '';

  const versioningInfoCopy = `Publish creates an immutable version. All triggers fire against the Live version only. Your draft only runs via 'Dry Run' and 'Run Current'. Set the new version's state (Active / Paused / Inactive) from the version history drawer.`;

  return (
    <Box
      sx={{
        display: 'flex',
        alignItems: 'center',
        gap: 1,
        // The header places centered tab pills (260px wide) absolutely on the
        // top axis; without a left buffer the strip's first child sits right
        // against the tab right edge so the unsaved-changes text appeared to
        // run into "Executions" in the screenshot. The padding keeps a safe
        // visual gap regardless of which chip ends up leftmost (unsaved /
        // draft / live).
        pl: 2,
      }}
      data-testid='workflow-state-strip'
    >
      {/* Unsaved-edits dot + draft chip share a Box wrapper so they stack as a
          single logical unit relative to the rest of the strip. */}
      <Box>
        {!isNewWorkflow && hasUnsavedChanges && (
          <Tooltip title='You have edits on the canvas that are not saved yet. Click Save draft to persist them.'>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }} data-testid='workflow-unsaved-indicator'>
              <Box
                sx={{
                  width: 8,
                  height: 8,
                  borderRadius: '50%',
                  backgroundColor: ds.amber[400],
                }}
              />
              <Typography sx={{ fontSize: '12px', fontWeight: 500, color: ds.gray[700], whiteSpace: 'nowrap' }}>Unsaved changes</Typography>
            </Box>
          </Tooltip>
        )}

        {/* Draft / unpublished chip. Two cases share one chip:
            (a) Checked-out from a non-live version (rollback / restore branch)
            (b) Saved draft on top of the live version that is ahead of live
            Case (b) keeps the warning visible after Save Draft — hasUnsavedChanges
            flips false but the draft is still unpublished. Driven by
            draftAheadOfLive (session-scoped flag in WorkflowBuilderNotebook). */}
        {!isNewWorkflow && draftChipLabel && (
          <Tooltip title={draftChipTooltip}>
            <Box sx={{ display: 'flex', alignItems: 'center' }} data-testid='workflow-draft-indicator'>
              <Chip size='xs' variant='tag' tone='warning'>
                {draftChipLabel}
              </Chip>
            </Box>
          </Tooltip>
        )}
      </Box>

      {/* Live version chip — always visible (when a live version exists) so the
          user knows which snapshot all triggers (scheduled, event, webhook,
          manual) run. Label spells out "Live version: vN" rather than the
          previous bare "Live vN" so it can't be mistaken for the draft chip
          ("Draft based on vN"). */}
      {!isNewWorkflow && hasLiveVersion && (
        <Tooltip title={`All triggers run the live version${liveVersionName ? ` (“${liveVersionName}”)` : ''}.`}>
          <Box sx={{ display: 'flex', alignItems: 'center' }} data-testid='workflow-live-indicator'>
            <Chip size='xs' variant='tag' tone='info'>
              {`Live version: v${liveVersionNumber ?? '?'}`}
            </Chip>
          </Box>
        </Tooltip>
      )}
      {!isNewWorkflow && !hasLiveVersion && (
        <Tooltip title='No live version yet. Publish to create one — all triggers run the live version.'>
          <Box sx={{ display: 'flex', alignItems: 'center' }} data-testid='workflow-live-indicator'>
            <Chip size='xs' variant='tag' tone='neutral'>
              No live version
            </Chip>
          </Box>
        </Tooltip>
      )}

      {/* Versioning info icon — explains the publish → live → trigger semantics
          users repeatedly stumble over. Hidden in create mode (the user is
          editing a brand-new workflow; the publish concept is introduced
          inline by the rename of "Save draft" → "Publish"). */}
      {!isNewWorkflow && (
        <Tooltip title={versioningInfoCopy}>
          <Box sx={{ display: 'flex', alignItems: 'center', color: ds.gray[500], cursor: 'help' }} data-testid='workflow-versioning-info'>
            <InfoOutlinedIcon sx={{ fontSize: 16 }} />
          </Box>
        </Tooltip>
      )}

      {/* Actions: Publish + History (moved here from the bottom action bar).
          Both use the DS Button so they share affordances with the rest of
          the workflow builder toolbar (Run Current / Save Draft / Settings /
          Prettify) instead of the legacy NewCustomButton, which had its own
          padding/typography that no longer matches. */}
      {!isNewWorkflow && (
        <>
          {onPublish && (
            <Button id='workflow-publish-btn' data-testid='workflow-publish-btn' onClick={onPublish} tone='primary' size='sm'>
              Publish
            </Button>
          )}
          {onHistory && (
            <Button
              id='workflow-history-btn'
              data-testid='workflow-history-btn'
              onClick={onHistory}
              tone='secondary'
              size='sm'
              icon={<HistoryIcon sx={{ fontSize: 16 }} />}
            >
              History
            </Button>
          )}
        </>
      )}
    </Box>
  );
};

export default WorkflowStateStrip;
