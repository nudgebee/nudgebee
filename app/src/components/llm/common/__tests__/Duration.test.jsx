import { render, screen } from '@testing-library/react';
import Duration from '@components/llm/common/Duration';

// Regression coverage for the `live` prop: a still-running row's `updated_at` stays
// pinned near `created_at` until the backend writes the final value, so the static
// updatedAt-createdAt diff reads "0ns" for the entire run and only jumps to the real
// total once it completes. `live` switches to a createdAt-anchored clock instead.
describe('Duration', () => {
  it('shows the static updatedAt - createdAt diff when not live', () => {
    render(<Duration createdAt='2026-01-01T00:00:00.000Z' updatedAt='2026-01-01T00:00:43.000Z' />);
    expect(screen.getByText('43s')).toBeInTheDocument();
  });

  it('ticks from createdAt instead of reading ~0 when live and updatedAt is stale', () => {
    const createdAt = new Date(Date.now() - 31000).toISOString(); // 31s ago
    const updatedAt = new Date(Date.now() - 30900).toISOString(); // barely after createdAt — stale "in progress" write
    render(<Duration createdAt={createdAt} updatedAt={updatedAt} live />);
    expect(screen.getByText('31s')).toBeInTheDocument();
  });
});
