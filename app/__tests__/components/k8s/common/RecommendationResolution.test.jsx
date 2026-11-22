import React from 'react';
import { render, screen } from '@testing-library/react';
import '@testing-library/jest-dom';

jest.mock('@api1/recommendation', () => ({
  __esModule: true,
  default: { listRecommendationResolution: jest.fn() },
}));
jest.mock('@api1/user', () => ({
  __esModule: true,
  default: { getUserPreferencesTablePageSize: jest.fn(() => 10), setUserPreferencesTablePageSize: jest.fn() },
}));

import recommendationApi from '@api1/recommendation';
import RecommendationResolution from '@components/k8s/common/RecommendationResolution';

// The resolver's name comes with the resolution rows (resolver_display_name,
// joined in the query engine within the tenant). The table used to look each
// resolver up by id through users_get_details, which read any tenant's users.
describe('RecommendationResolution', () => {
  it('shows the resolver name from the resolution rows', async () => {
    recommendationApi.listRecommendationResolution.mockResolvedValue({
      data: {
        recommendation_resolution: [
          {
            id: 'r1',
            type: 'Ticket',
            resolver_id: 'u1',
            resolver_type: 'User',
            resolver_display_name: 'Asha Rao',
            status: 'Success',
            updated_at: '2026-10-24T10:00:00Z',
          },
          { id: 'r2', type: 'AutoPilot', resolver_id: '', resolver_type: 'AutoPilot', status: 'Success', updated_at: '2026-10-24T09:00:00Z' },
        ],
        recommendation_resolution_aggregate: { aggregate: { count: 2 } },
      },
    });

    render(<RecommendationResolution recommendation={{ id: 'rec-1' }} />);

    expect(await screen.findByText('Asha Rao')).toBeInTheDocument();
    expect(screen.getAllByText('AutoPilot').length).toBeGreaterThan(0);
  });
});
