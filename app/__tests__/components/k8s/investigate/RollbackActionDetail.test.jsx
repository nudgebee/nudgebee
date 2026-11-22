import React from 'react';
import { render, screen } from '@testing-library/react';
import '@testing-library/jest-dom';

jest.mock('@api1/recommendation', () => ({ __esModule: true, default: { checkEventRollback: jest.fn() } }));

import apiRecommendations from '@api1/recommendation';
import RollbackActionDetail from '@components/k8s/investigate/RollbackActionDetail';

const LINE = '2 min before the alert · rolls back readinessProbe.exec.command[0] false → true · writes back the values this change replaced';
const BLOCKED = '2 min before the alert · changed again since, so this change cannot be rolled back on its own';

const renderRow = () => render(<RollbackActionDetail accountId='acc' eventId='alert-1' changeEventId='c1' line={LINE} blockedLine={BLOCKED} />);

// The Remediation row for a single change prints that change's recorded values as "running now →
// after rollback". After a later change that was false, and the dialog it opened said the same.
describe('rollback row detail', () => {
  beforeEach(() => jest.clearAllMocks());

  it('states what the rollback writes when nothing changed the workload since', async () => {
    apiRecommendations.checkEventRollback.mockResolvedValue({ allowed: true, reason: '' });
    renderRow();
    expect(await screen.findByText(LINE)).toBeInTheDocument();
    expect(apiRecommendations.checkEventRollback).toHaveBeenCalledWith('acc', 'alert-1', 'c1');
  });

  it('says the change was changed again instead of claiming what is running now', async () => {
    apiRecommendations.checkEventRollback.mockResolvedValue({ allowed: false, reason: 'event resolution: "x" was changed again by a later change' });
    renderRow();
    expect(await screen.findByText(BLOCKED)).toBeInTheDocument();
    expect(screen.queryByText(LINE)).not.toBeInTheDocument();
  });

  it('keeps the row as it was when the check could not be answered', async () => {
    apiRecommendations.checkEventRollback.mockResolvedValue(null);
    renderRow();
    expect(await screen.findByText(LINE)).toBeInTheDocument();
  });

  // The page re-renders this row on every resolutions poll; that must not become a request each time.
  it('asks once per change, not once per render', async () => {
    apiRecommendations.checkEventRollback.mockResolvedValue({ allowed: true, reason: '' });
    const { rerender } = renderRow();
    await screen.findByText(LINE);
    rerender(<RollbackActionDetail accountId='acc' eventId='alert-1' changeEventId='c1' line={LINE} blockedLine={BLOCKED} />);
    rerender(<RollbackActionDetail accountId='acc' eventId='alert-1' changeEventId='c1' line={LINE} blockedLine={BLOCKED} />);
    expect(apiRecommendations.checkEventRollback).toHaveBeenCalledTimes(1);
  });
});
