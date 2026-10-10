import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';

jest.mock('@api1/recommendation', () => ({ __esModule: true, default: { applyRecommendation: jest.fn(), checkEventRollback: jest.fn() } }));
jest.mock('@api1/kubernetes', () => ({ __esModule: true, default: { resolveEventRecord: jest.fn() } }));

import apiKubernetes from '@api1/kubernetes';
import apiRecommendations from '@api1/recommendation';
import InvestigateResolution from '@components/k8s/investigate/InvestigateResolution';

// Unless a test says otherwise, the server finds no later change on the workload.
beforeEach(() => {
  jest.clearAllMocks();
  apiRecommendations.checkEventRollback.mockResolvedValue({ allowed: true, reason: '' });
});

// The manifests the collector records are serialised snake_case — owner references included.
const manifest = ({ labels = {}, annotations = {}, ownerReferences = '' } = {}) => {
  const block = (name, map) =>
    Object.keys(map).length
      ? `  ${name}:\n${Object.entries(map)
          .map(([k, v]) => `    ${k}: ${v}`)
          .join('\n')}\n`
      : '';
  return `metadata:\n${block('annotations', annotations)}${block('labels', labels)}${ownerReferences}  name: notifications\n  namespace: nudgebee\n`;
};

const eventWith = (resourceName, newManifest) => ({
  id: 'alert-1',
  cloud_account_id: 'acc',
  subject_namespace: 'nudgebee',
  starts_at: '2026-09-18T00:14:11Z',
  evidences: [
    {
      type: 'diff',
      start_at: '2026-09-16T17:12:28Z',
      data: {
        resource_name: resourceName,
        old: 'a',
        new: newManifest,
        updated_values: [
          { path: 'spec.template.metadata.annotations.rollme', old: 'FvTKN', new: 'Uf5yp' },
          { path: 'spec.template.spec.containers[0].image', old: 'registry/app:v1', new: 'registry/app:v2' },
        ],
      },
    },
  ],
});

const renderDialog = (row) =>
  render(
    <InvestigateResolution
      row={row}
      handleClose={jest.fn()}
      updateInvestigateSuccessSnackBar={jest.fn()}
      isRevertTheDevelopment
      cardId='LastDeploymentCard_0'
    />
  );

describe('rollback dialog', () => {
  it('names the target, the timing and each field as now vs after rollback', async () => {
    renderDialog(eventWith('deployment/nudgebee/notifications.yaml', manifest()));
    expect(screen.getByText('Deployment nudgebee/notifications')).toBeInTheDocument();
    expect(screen.getByText(/31h before the alert/)).toBeInTheDocument();
    // The image is confirmed live; the restart marker is not checked, so it is never called "Now".
    expect(await screen.findAllByText('Now')).toHaveLength(1);
    expect(screen.getAllByText('At change time')).toHaveLength(1);
    expect(screen.getAllByText('After rollback')).toHaveLength(2);
    // Bare values, not JSON-quoted.
    expect(screen.getByText('registry/app:v2')).toBeInTheDocument();
    expect(screen.getByText('registry/app:v1')).toBeInTheDocument();
    expect(screen.getByText(/restart marker/)).toBeInTheDocument();
    expect(screen.getByText(/one by one/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Roll back' })).toBeEnabled();
  });

  it('warns that Helm will re-apply the change and offers helm rollback', async () => {
    renderDialog(
      eventWith(
        'deployment/nudgebee/notifications.yaml',
        manifest({ labels: { 'app.kubernetes.io/managed-by': 'Helm' }, annotations: { 'meta.helm.sh/release-name': 'notifications' } })
      )
    );
    expect(screen.getByText(/helm rollback notifications -n nudgebee/)).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Roll back' })).toBeEnabled());
  });

  // The fields are what the change recorded. A deployment changed again after the event still read
  // "Now" with the older value, and the operator learned of the later change only from the refusal
  // that followed the click.
  it('warns before the click when the workload was changed again, and does not offer the rollback', async () => {
    apiRecommendations.checkEventRollback.mockResolvedValue({
      allowed: false,
      reason:
        'event resolution: "spec.template.spec.containers[0].image" was changed again by a later change (2026-09-17T10:19:38Z) — reverting this one would undo that too. Revert the later change first.',
    });
    renderDialog(eventWith('deployment/nudgebee/notifications.yaml', manifest()));

    const warning = await screen.findByTestId('rollback-blocked-reason');
    expect(warning).toHaveTextContent('"spec.template.spec.containers[0].image" was changed again by a later change (2026-09-17T10:19:38Z)');
    expect(warning).not.toHaveTextContent('event resolution:');
    expect(apiRecommendations.checkEventRollback).toHaveBeenCalledWith('acc', 'alert-1', '');
    // The recorded value is no longer what is running, so it is not called "Now".
    expect(screen.queryByText('Now')).not.toBeInTheDocument();
    expect(screen.getAllByText('At change time')).toHaveLength(2);
    const rollBack = screen.getByRole('button', { name: 'Roll back' });
    expect(rollBack).toBeDisabled();
    fireEvent.click(rollBack);
    expect(apiRecommendations.applyRecommendation).not.toHaveBeenCalled();
  });

  // An unanswered check (a transport failure, an older server) is not a block: the apply runs the
  // same check on the click. It is not a confirmation either, so the value is not called "Now".
  it('still offers the rollback when the check could not be answered', async () => {
    apiRecommendations.checkEventRollback.mockResolvedValue(null);
    renderDialog(eventWith('deployment/nudgebee/notifications.yaml', manifest()));

    await waitFor(() => expect(screen.getByRole('button', { name: 'Roll back' })).toBeEnabled());
    expect(screen.queryByTestId('rollback-blocked-reason')).not.toBeInTheDocument();
    expect(screen.queryByText('Now')).not.toBeInTheDocument();
    expect(screen.getAllByText('At change time')).toHaveLength(2);
  });

  it('does not offer the rollback until the check has answered', async () => {
    let answer;
    apiRecommendations.checkEventRollback.mockReturnValue(new Promise((resolve) => (answer = resolve)));
    renderDialog(eventWith('deployment/nudgebee/notifications.yaml', manifest()));

    expect(screen.getByRole('button', { name: 'Roll back' })).toBeDisabled();
    answer({ allowed: true, reason: '' });
    await waitFor(() => expect(screen.getByRole('button', { name: 'Roll back' })).toBeEnabled());
  });

  // vmagent: the VMAgent operator rewrites the Deployment within seconds, so the rollback would
  // "succeed" and change nothing.
  it('blocks a rollback of an operator-owned workload', async () => {
    const owner =
      '  owner_references:\n  - api_version: operator.victoriametrics.com/v1beta1\n    controller: true\n    kind: VMAgent\n    name: victoria\n';
    renderDialog(eventWith('deployment/victoria/vmagent.yaml', manifest({ ownerReferences: owner })));
    expect(screen.getByText(/VMAgent victoria owns this workload/)).toBeInTheDocument();
    // Still blocked once the later-change check has answered "nothing changed since".
    expect(await screen.findAllByText('Now')).toHaveLength(1);
    expect(screen.getByRole('button', { name: 'Roll back' })).toBeDisabled();
  });

  it('names a DaemonSet as one and says it restarts on every node', () => {
    renderDialog(eventWith('daemonset/agent/node-agent.yaml', manifest()));
    expect(screen.getByText('DaemonSet agent/node-agent')).toBeInTheDocument();
    expect(screen.getByText(/every node/)).toBeInTheDocument();
  });

  // Several changes: the operator picks in the dialog, and the preview follows the pick.
  it('lets the operator pick the change', async () => {
    const changeEvent = (image) => ({
      data: {
        events: [
          {
            id: 'x',
            starts_at: '2026-09-16T12:00:00Z',
            evidences: [
              {
                type: 'diff',
                data: {
                  resource_name: 'deployment/nudgebee/notifications.yaml',
                  old: 'a',
                  new: manifest(),
                  updated_values: [{ path: 'spec.template.spec.containers[0].image', old: `${image}-old`, new: image }],
                },
              },
            ],
          },
        ],
      },
    });
    apiKubernetes.resolveEventRecord.mockImplementation((id) => Promise.resolve(changeEvent(id === 'c2' ? 'env-change' : 'image-change')));
    const choices = [
      { changeEventId: 'c3', when: '16 Sep 22:42', timing: '31h before the alert', summary: 'image' },
      { changeEventId: 'c2', when: '16 Sep 17:30', timing: '36h before the alert', summary: 'env' },
    ];
    render(
      <InvestigateResolution
        row={{ id: 'alert-1', cloud_account_id: 'acc', starts_at: '2026-09-18T00:14:11Z', evidences: [] }}
        handleClose={jest.fn()}
        updateInvestigateSuccessSnackBar={jest.fn()}
        isRevertTheDevelopment
        cardId='LastDeploymentCard_0'
        changeEventId='c3'
        changeChoices={choices}
      />
    );
    expect(await screen.findByText('image-change')).toBeInTheDocument();
    const radios = screen.getAllByRole('radio');
    expect(radios).toHaveLength(2);
    fireEvent.click(radios[1]);
    await waitFor(() => expect(apiKubernetes.resolveEventRecord).toHaveBeenLastCalledWith('c2', 'acc'));
    expect(await screen.findByText('env-change')).toBeInTheDocument();
    // Each pick is checked for itself: a later change may block one change and not another.
    expect(apiRecommendations.checkEventRollback).toHaveBeenCalledWith('acc', 'alert-1', 'c3');
    expect(apiRecommendations.checkEventRollback).toHaveBeenLastCalledWith('acc', 'alert-1', 'c2');
  });
});

// The header's "Resolve Event" dialog had rollback controls of its own — a Revert radio on CPU and
// memory alerts, a Revert checkbox on image-pull alerts — sent to server branches that never rolled
// anything back. The Remediation row is the one place a rollback is started.
describe('resolve event dialog', () => {
  const resolveDialog = (aggregationKey) =>
    render(
      <InvestigateResolution
        row={{ ...eventWith('deployment/nudgebee/notifications.yaml', manifest()), aggregation_key: aggregationKey }}
        handleClose={jest.fn()}
        updateInvestigateSuccessSnackBar={jest.fn()}
      />
    );

  it.each(['CPUThrottlingHigh', 'PodMemoryReachingLimit'])('offers no rollback of its own on %s', (aggregationKey) => {
    resolveDialog(aggregationKey);
    expect(screen.getByLabelText('Restart')).toBeInTheDocument();
    expect(screen.queryByLabelText('Revert')).not.toBeInTheDocument();
    expect(apiRecommendations.checkEventRollback).not.toHaveBeenCalled();
  });

  it('offers no rollback of its own on an image-pull alert', () => {
    resolveDialog('image_pull_backoff_reporter');
    expect(screen.getByText('Mention Container Name and Image Tag to change')).toBeInTheDocument();
    expect(screen.queryByText('Revert the Deployment')).not.toBeInTheDocument();
  });
});
