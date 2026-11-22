import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { Box } from '@mui/material';
import { Modal } from '@ui/Modal';
import { Button } from '@ui/Button';
import { Input } from '@ui/Input';
import { Select } from '@ui/Select';
import { toast as snackbar } from '@ui/Toast';
import k8sApi from '@api1/kubernetes';
import apiTickets, { GitIntegration } from '@api1/tickets';
import { ANNOTATIONS, serviceAnnotationKey } from '@lib/annotationKeys';
import { ds } from '@utils/colors';

export interface VmServiceRepo {
  repo: string;
  branch: string;
}

/** Folds a VM's attributes into the repo and branch linked to each service, keyed by container_id. */
export function parseServiceRepos(rows?: { name: string; value: string }[]): Record<string, VmServiceRepo> {
  const links: Record<string, VmServiceRepo> = {};
  for (const { name, value } of rows ?? []) {
    for (const [field, key] of [
      ['repo', ANNOTATIONS.WORKLOAD_GIT_REPO],
      ['branch', ANNOTATIONS.WORKLOAD_GIT_BRANCH],
    ] as const) {
      if (name.startsWith(`${key}/`)) {
        const containerId = name.slice(key.length + 1);
        links[containerId] = { ...(links[containerId] ?? { repo: '', branch: '' }), [field]: value || '' };
      }
    }
  }
  return links;
}

/** The repo and branch linked to each service of a VM. A cleared link keeps its rows with an empty value. */
export function useVmServiceRepos(vmId: string) {
  const [links, setLinks] = useState<Record<string, VmServiceRepo>>({});

  const reload = useCallback(async () => {
    setLinks(parseServiceRepos(await k8sApi.getResourceAttributes(vmId)));
  }, [vmId]);

  useEffect(() => {
    reload();
  }, [reload]);

  return { links, reload };
}

interface VmServiceGitModalProps {
  accountId: string;
  vmId: string;
  containerId: string;
  serviceName: string;
  current?: VmServiceRepo;
  onClose: () => void;
  onSaved: () => void;
}

/** Sets the repo and branch a VM service runs. An empty repo clears the link. */
export const VmServiceGitModal = ({ accountId, vmId, containerId, serviceName, current, onClose, onSaved }: VmServiceGitModalProps) => {
  const [repo, setRepo] = useState(current?.repo || '');
  const [branch, setBranch] = useState(current?.branch || '');
  const [integrations, setIntegrations] = useState<GitIntegration[]>([]);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    apiTickets
      .listGitIntegrations()
      .then(setIntegrations)
      .catch(() => setIntegrations([]));
  }, []);

  // One flat list of every repo the connected integrations can read, so a user picks in one step.
  const repoOptions = useMemo(
    () => integrations.flatMap((i) => i.projects.map((p) => ({ label: `${i.label} / ${p.name}`, url: `${i.url}/${p.key}` }))),
    [integrations]
  );

  const trimmedRepo = repo.trim();
  const repoError = trimmedRepo && !/^https?:\/\//.test(trimmedRepo) ? 'Use the full URL, e.g. https://github.com/org/repo' : undefined;

  // code_analyzer drops a repo no connected integration can read and auto-detects another, so say so before saving.
  const normalizeRepo = (url: string) => url.replace(/(\.git)?\/*$/, '').toLowerCase();
  const repoNotConnected =
    !!trimmedRepo && !repoError && !repoOptions.some((o) => normalizeRepo(o.url) === normalizeRepo(trimmedRepo))
      ? 'This repository is not in a connected integration, so investigations will not read it. Connect it under Integrations first.'
      : undefined;

  const save = async () => {
    setSaving(true);
    try {
      await k8sApi.upsertResourceAttributes(vmId, accountId, [
        { name: serviceAnnotationKey(ANNOTATIONS.WORKLOAD_GIT_REPO, containerId), value: trimmedRepo },
        { name: serviceAnnotationKey(ANNOTATIONS.WORKLOAD_GIT_BRANCH, containerId), value: trimmedRepo ? branch.trim() : '' },
      ]);
      snackbar.success('Repository saved');
      onSaved();
      onClose();
    } catch (error: any) {
      snackbar.error(error?.message || 'Failed to save the repository');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      open
      handleClose={onClose}
      title={`Repository - ${serviceName}`}
      width='md'
      actionButtons={
        <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: ds.space[3], p: `${ds.space[3]} ${ds.space[5]}` }}>
          <Button id='vm-service-git-cancel-btn' tone='secondary' size='md' onClick={onClose}>
            Cancel
          </Button>
          <Button id='vm-service-git-save-btn' tone='primary' size='md' onClick={save} disabled={saving || !!repoError}>
            Save
          </Button>
        </Box>
      }
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[4] }}>
        {repoOptions.length > 0 && (
          <Select
            label='Pick from connected repositories'
            placeholder='Select a repository'
            value={repoOptions.find((o) => o.url === trimmedRepo)?.label || null}
            options={repoOptions.map((o) => o.label)}
            onChange={(label: string) => setRepo(repoOptions.find((o) => o.label === label)?.url || repo)}
          />
        )}
        <Input
          size='sm'
          label='Repository URL'
          placeholder='https://github.com/org/repo'
          value={repo}
          onChange={setRepo}
          error={repoError}
          help={repoNotConnected || 'Leave empty to remove the link. The repository must belong to a connected GitHub or GitLab integration.'}
        />
        <Input size='sm' label='Branch' placeholder="Repository's default branch" value={branch} onChange={setBranch} disabled={!trimmedRepo} />
      </Box>
    </Modal>
  );
};
