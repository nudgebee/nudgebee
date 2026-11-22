import React, { useEffect, useState } from 'react';
import { Box, Stack, Typography } from '@mui/material';
import apiAutoPilot from '@api1/autoPilot';
import { Modal } from '@ui/Modal';
import { Button } from '@ui/Button';
import { Banner } from '@ui/Banner';
import { Input } from '@ui/Input';
import CustomTable from '@shared/tables/CustomTable';
import Datetime from '@shared/format/Datetime';
import { toast as snackbar } from '@ui/Toast';
import { ds } from '@utils/colors';
import { parseRecommendationMeta } from '@components/autopilot/taskChanges';
import { decisionsRecord } from '@api1/memory';

export interface RunApproval {
  id: string;
  autopilot_id: string;
  created_at: string;
  attributes?: { task_ids?: string[]; expires_at?: string };
}

interface AutoOptimizeRunReviewModalProps {
  open: boolean;
  approval: RunApproval | null;
  automationName: string;
  accountId: string;
  onClose: (decided: boolean) => void;
}

type Cell = { text?: React.ReactNode; component?: React.ReactNode };

const workloadLabel = (task: any) => {
  const rf = task?.resource_filter && Object.keys(task.resource_filter).length ? task.resource_filter : task?.meta ?? {};
  const kind = rf.type ?? rf.kind;
  return [rf.namespace, kind, rf.name].filter(Boolean).join('/') || task?.name || '-';
};

const ChangeList: React.FC<{ meta: any; reason?: string }> = ({ meta, reason }) => {
  const rows: Cell[][] = parseRecommendationMeta(meta);
  if (!rows.length) {
    return <Typography sx={{ fontSize: ds.text.body, color: ds.gray[600] }}>{reason || '-'}</Typography>;
  }
  return (
    <Stack gap={ds.space[1]}>
      {rows.map((row, i) => (
        <Box key={i} sx={{ display: 'flex', gap: ds.space[2], fontSize: ds.text.body, color: ds.gray[700] }}>
          <Box sx={{ minWidth: 140, color: ds.gray[500] }}>{row[0]?.text}</Box>
          <Box>{row[1]?.component ?? row[1]?.text}</Box>
          <Box sx={{ color: ds.gray[400] }}>→</Box>
          <Box sx={{ fontWeight: ds.weight.medium }}>{row[2]?.component ?? row[2]?.text}</Box>
        </Box>
      ))}
    </Stack>
  );
};

const AutoOptimizeRunReviewModal: React.FC<AutoOptimizeRunReviewModalProps> = ({ open, approval, automationName, accountId, onClose }) => {
  const [tasks, setTasks] = useState<any[]>([]);
  const [loading, setLoading] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [comment, setComment] = useState('');
  const [commentError, setCommentError] = useState('');

  useEffect(() => {
    if (!open || !approval) return;
    let cancelled = false;
    setComment('');
    setCommentError('');
    setLoading(true);
    apiAutoPilot
      .getApprovalTasks(approval.attributes?.task_ids ?? [])
      .then((rows: any[]) => {
        if (!cancelled) setTasks(rows);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, approval?.id]);

  const decide = async (status: 'APPROVED' | 'REJECTED') => {
    if (!approval) return;
    if (status === 'REJECTED' && !comment.trim()) {
      setCommentError('Add a comment so the team knows why these changes were rejected.');
      return;
    }
    setSubmitting(true);
    try {
      const res = await apiAutoPilot.decideAutoOptimizeApproval(approval.id, accountId, status, comment.trim());
      if (res?.errors?.length) {
        snackbar.error(res.errors[0]?.message || 'Could not record the decision.');
        onClose(true);
        return;
      }
      snackbar.success(res?.data?.message || (status === 'APPROVED' ? 'Approved.' : 'Rejected.'));
      decisionsRecord({
        decisionType: status === 'APPROVED' ? 'action_approved' : 'action_declined',
        subject: `rightsize: ${automationName}`,
        rationale: comment.trim() || (status === 'APPROVED' ? 'approved without review comment' : 'declined without review comment'),
        agentModule: 'automation',
      }).catch(() => {
        // Best-effort memory write; never blocks the decision.
      });
      onClose(true);
    } finally {
      setSubmitting(false);
    }
  };

  const tableData: Cell[][] = tasks.map((task) => [
    { text: workloadLabel(task) },
    { component: <ChangeList meta={task?.meta} reason={task?.reason} /> },
  ]);
  const count = approval?.attributes?.task_ids?.length ?? 0;

  return (
    <Modal
      open={open}
      handleClose={() => onClose(false)}
      title={`Review planned changes — ${automationName}`}
      width='md'
      loader={submitting}
      backdropClickClose={!submitting}
      actionButtons={
        <Stack direction='row' gap={ds.space[3]} sx={{ button: { minWidth: '140px' } }}>
          <Button tone='secondary' onClick={() => decide('REJECTED')} disabled={submitting || loading}>
            Reject
          </Button>
          <Button onClick={() => decide('APPROVED')} disabled={submitting || loading || count === 0}>
            Approve and apply
          </Button>
        </Stack>
      }
    >
      <Stack gap={ds.space[3]} sx={{ p: ds.space[4] }}>
        <Banner
          tone='info'
          surface='section'
          message={`Nothing has been applied yet. Approving applies these ${count} change(s) now. A change whose recommendation has moved since this plan was made is skipped.`}
        />
        <Box sx={{ display: 'flex', gap: ds.space[5], fontSize: ds.text.body, color: ds.gray[600] }}>
          <Box>
            Planned <Datetime value={approval?.created_at} />
          </Box>
          {approval?.attributes?.expires_at && (
            <Box>
              Expires <Datetime value={approval.attributes.expires_at} />
            </Box>
          )}
        </Box>
        <CustomTable
          id='auto-optimize-run-review'
          headers={[
            { name: 'Workload', width: '40%' },
            { name: 'Planned change', width: '60%' },
          ]}
          tableData={tableData}
          loading={loading}
          rowsPerPage={tableData.length || 10}
        />
        <Input
          type='textarea'
          label='Comment'
          placeholder='Optional when approving, required when rejecting'
          value={comment}
          onChange={(next) => {
            setComment(next);
            if (commentError) setCommentError('');
          }}
          error={commentError}
          minRows={2}
          disabled={submitting}
        />
      </Stack>
    </Modal>
  );
};

export default AutoOptimizeRunReviewModal;
