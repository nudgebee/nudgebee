import { render, screen } from '@testing-library/react';
import ImpactPanel from '@shared/widgets/ImpactPanel';

jest.mock('@api1/kubernetes1', () => ({ getImpactData: jest.fn() }));
jest.mock('next/router', () => ({ useRouter: () => ({ push: jest.fn() }) }));

// The impact tier holds one row per ALERT but is describing SERVICES. On a live
// Rackspace incident two machines firing several alarms each rendered as
// "8 services" — which reads as eight things broken when two were. The whole card
// exists to answer "what did this affect", so a wrong number there is worse than
// no number: someone who checks it once and finds it wrong stops believing the
// rest of the panel.
const impactRows = [
  { event_id: 'e1', subject: '|i-0b079820a95b1517a', title: 'payment-down-v2', dt_seconds: 120 },
  { event_id: 'e2', subject: '|i-0b079820a95b1517a', title: 'payment-down', dt_seconds: 120 },
  { event_id: 'e3', subject: '|i-0b079820a95b1517a', title: 'payment-cpu', dt_seconds: 60 },
  { event_id: 'e4', subject: '|i-00e845d10772a9058', title: 'inventory-down', dt_seconds: 60 },
];

const prefetched = (impact: unknown[]) => ({
  resolved: true,
  seed: { name: 'i-0dcee3621b8456783' },
  impacted: [],
  infrastructure_impacted: [],
  assembly: { same_incident: [], cause: { config_changes: [], upstream: [] }, impact, chronic: [] },
});

describe('impact tier count', () => {
  it('reports services and alerts separately when a service fired more than once', () => {
    render(<ImpactPanel eventId='seed' prefetched={prefetched(impactRows)} />);
    // Four rows, two machines. Saying "4 services" would be the bug; saying only
    // "2 services" would hide that there are four rows below.
    expect(screen.getByText('2 services, 4 alerts')).toBeInTheDocument();
    expect(screen.queryByText('4 services')).not.toBeInTheDocument();
  });

  it('says it plainly when each service fired once', () => {
    render(<ImpactPanel eventId='seed' prefetched={prefetched(impactRows.slice(2))} />);
    // One payment row, one inventory row: two services, two alerts. Printing both
    // numbers here would be noise, so it collapses to the thing that matters.
    expect(screen.getByText('2 services')).toBeInTheDocument();
  });

  it('does not pluralise a single service', () => {
    render(<ImpactPanel eventId='seed' prefetched={prefetched([impactRows[3]])} />);
    expect(screen.getByText('1 service')).toBeInTheDocument();
  });

  // A row with no subject must not be merged into another service's count — that
  // would under-report the blast radius, which is the more dangerous direction.
  it('counts a row with no subject as its own service', () => {
    const rows = [impactRows[3], { event_id: 'e9', title: 'orphan', dt_seconds: 30 }];
    render(<ImpactPanel eventId='seed' prefetched={prefetched(rows)} />);
    expect(screen.getByText('2 services')).toBeInTheDocument();
  });
});
