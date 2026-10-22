import React from 'react';
import { render, screen } from '@testing-library/react';
import '@testing-library/jest-dom';

jest.mock('@api1/recommendation', () => ({ __esModule: true, default: { applyRecommendation: jest.fn(), checkEventRollback: jest.fn() } }));
jest.mock('@api1/kubernetes', () => ({ __esModule: true, default: { resolveEventRecord: jest.fn() } }));

import apiRecommendations from '@api1/recommendation';
import InvestigateResolution from '@components/k8s/investigate/InvestigateResolution';
import { RESOLVABLE_ALERT_KEYS } from '@data/investigateConstants';

const CPU_FORECAST_KEYS = ['CPUThrottlingPredicted', 'CPUSaturationPredicted', 'CPUThrottlingSLOBreached', 'CPUSaturationSLOBreached'];

beforeEach(() => {
  jest.clearAllMocks();
  apiRecommendations.checkEventRollback.mockResolvedValue({ allowed: true, reason: '' });
});

const renderFix = (aggregationKey) =>
  render(
    <InvestigateResolution
      row={{ id: 'alert-1', cloud_account_id: 'acc', subject_namespace: 'shop', aggregation_key: aggregationKey, evidences: [] }}
      handleClose={jest.fn()}
      updateInvestigateSuccessSnackBar={jest.fn()}
    />
  );

describe('CPU SLO forecast alerts', () => {
  it.each(CPU_FORECAST_KEYS)('%s offers the Resolve Event action', (key) => {
    expect(RESOLVABLE_ALERT_KEYS).toContain(key);
  });

  it.each(CPU_FORECAST_KEYS)('%s offers the CPU fixes: raise the limit, add replicas or restart', (key) => {
    renderFix(key);
    expect(screen.getByText('Increase CPU To')).toBeInTheDocument();
    expect(screen.getByText('Increase Replicas')).toBeInTheDocument();
    expect(screen.getByText('Restart')).toBeInTheDocument();
  });

  it.each(['PodMemoryReachingLimit', 'MemorySaturationPredicted', 'MemorySaturationSLOBreached'])('%s offers the memory fixes', (key) => {
    expect(RESOLVABLE_ALERT_KEYS).toContain(key);
    renderFix(key);
    expect(screen.getByText('Increase Memory To')).toBeInTheDocument();
    expect(screen.queryByText('Increase CPU To')).not.toBeInTheDocument();
  });
});
