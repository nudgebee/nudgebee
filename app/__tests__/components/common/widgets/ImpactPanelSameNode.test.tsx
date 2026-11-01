import { render, screen } from '@testing-library/react';
import ImpactPanel from '@shared/widgets/ImpactPanel';

jest.mock('@api1/kubernetes1', () => ({ getImpactData: jest.fn() }));
jest.mock('next/router', () => ({ useRouter: () => ({ push: jest.fn() }) }));

// When a node runs out of memory it kills pods in several namespaces. They share
// no service-map edge with the alert you opened, so they must not be filed under
// "More alerts on <this service>" — they get a section that says what they are.
const sameNodeRows = [
  { event_id: 'n1', subject: 'monitoring|metrics-db', title: 'metrics-db OOM killed', relation: 'same_node', dt_seconds: -10 },
  { event_id: 'n2', subject: '|node-1', title: 'node_pressure on node-1', relation: 'same_node', dt_seconds: 40 },
];

const prefetched = (sameNode: unknown[] | undefined) => ({
  resolved: true,
  seed: { name: 'log-agent' },
  impacted: [],
  infrastructure_impacted: [],
  assembly: { same_incident: [], cause: { config_changes: [], upstream: [] }, impact: [], chronic: [], same_node: sameNode },
});

describe('same node section', () => {
  it('lists the other alerts from the node under their own heading', () => {
    render(<ImpactPanel eventId='seed' prefetched={prefetched(sameNodeRows)} />);
    expect(screen.getByText('Same node ran out of memory')).toBeInTheDocument();
    // Counted once on the section and once in the headline: they are part of the
    // one problem, and none of them is "on" the seed.
    expect(screen.getAllByText('2 alerts')).toHaveLength(2);
    expect(screen.getByText('2 alerts', { selector: 'b' })).toBeInTheDocument();
    expect(screen.queryByText('More alerts on log-agent')).not.toBeInTheDocument();
  });

  it('is absent when the server sends no such alerts, or predates the field', () => {
    const withImpact = (sameNode: unknown[] | undefined) => {
      const p = prefetched(sameNode);
      p.assembly.impact = [{ event_id: 'e1', subject: 'shop|api', title: 'api-down', dt_seconds: 60 }] as never[];
      return p;
    };
    const { unmount } = render(<ImpactPanel eventId='seed' prefetched={withImpact([])} />);
    expect(screen.queryByText('Same node ran out of memory')).not.toBeInTheDocument();
    unmount();
    render(<ImpactPanel eventId='seed' prefetched={withImpact(undefined)} />);
    expect(screen.queryByText('Same node ran out of memory')).not.toBeInTheDocument();
  });
});
