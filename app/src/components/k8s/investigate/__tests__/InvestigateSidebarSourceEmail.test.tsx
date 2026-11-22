import '@testing-library/jest-dom';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import InvestigateSidebar, { sourceEmailBody } from '@components/k8s/investigate/InvestigateSidebar';
import apiOwnership from '@api1/ownership';

/*
 * The "Source email" disclosure: an event raised from an alert email shows the mail it came from.
 */

jest.mock('@api1/ownership', () => ({ __esModule: true, default: { resolveOwners: jest.fn() } }));

jest.mock('@lib/auth', () => ({
  __esModule: true,
  isTenantAdmin: jest.fn(() => false),
  hasWriteAccess: jest.fn(() => true),
  getUserSession: jest.fn(() => ({})),
}));

jest.mock('next/router', () => ({
  useRouter: () => ({ events: { on: jest.fn(), off: jest.fn() }, push: jest.fn(), replace: jest.fn(), asPath: '/investigate', query: {} }),
}));

// Nothing here is about the event's state, its actions or its history.
jest.mock('@components/k8s/investigate/InvestigateDropdown', () => ({ __esModule: true, default: () => null }));
jest.mock('@components/k8s/investigate/EventStateRows', () => ({
  __esModule: true,
  EventOccurrenceRows: () => null,
  EventStateRows: () => null,
  useEventOccurrence: () => ({}),
}));
jest.mock('@components/k8s/investigate/AlertNotifications', () => ({
  __esModule: true,
  AlertNotifications: () => null,
  useAlertDeliveries: () => ({ visible: false, rows: [], historyStart: null, error: '' }),
}));

// The viewer's own rendering is not under test; what it is handed is.
jest.mock('@shared/viewers/MarkDowns', () => ({
  __esModule: true,
  default: ({ data, canRunCode }: { data: string; canRunCode?: boolean }) => (
    <div data-testid='markdown' data-can-run-code={String(canRunCode)}>
      {data}
    </div>
  ),
}));

const resolveOwners = apiOwnership.resolveOwners as jest.Mock;

const BODY = 'Alert for the Host app-01 was TRIGGERED at Oct 22, 2026 00:28:55 (IST). system.uptime: 0';

// The shape the email-ingest automation writes: a parse summary, then the mail.
const DESCRIPTION = `| | |\n|---|---|\n| Affected | app-01 |\n\n**Original email** (text, first 1,500 characters)\n\n\`\`\`\n${BODY}\n\`\`\`\n`;

const emailLabels = {
  nb_ingest_path: 'email',
  email_subject: 'Alert TRIGGERED: Critical - Host unreachable',
  email_from: 'alert@my.cloud.solarwinds.com',
  email_received_at: '2026-10-21T18:58:55.487Z',
};

const emailRow = { subject_type: 'host', subject_name: 'app-01', description: DESCRIPTION, labels: emailLabels };

const renderSidebar = (row: object) =>
  render(
    <InvestigateSidebar
      row={{ id: 'evt-1', cloud_account_id: 'acct-1', ...row }}
      isCloud
      sourceKnown
      othersData={[]}
      alertLabels={{}}
      alertRules={[]}
      queryParam={{}}
    />
  );

const settled = () => waitFor(() => expect(screen.queryByText('Resolving…')).not.toBeInTheDocument());

const openSourceEmail = async (row: object) => {
  renderSidebar(row);
  await settled();
  fireEvent.click(screen.getByTestId('toggle-source-email-btn'));
  return screen.getByTestId('sidebar-source-email');
};

describe('sourceEmailBody', () => {
  it('takes the mail out of the description', () => {
    expect(sourceEmailBody(DESCRIPTION)).toBe(BODY);
  });

  it('finds nothing in a description with no mail in it', () => {
    expect(sourceEmailBody('disk full on app-01')).toBe('');
    expect(sourceEmailBody(undefined)).toBe('');
  });

  // The automation rewrites every ``` in the mail as ''' before fencing it, so a
  // code block quoted in an alert can never close the fence early.
  it('keeps a code block quoted in the mail, which arrives with its fences rewritten', () => {
    const quoted = `${BODY}\n'''python\nprint("hello")\n'''\nThanks!`;
    const description = `**Original email** (first 1,500 characters)\n\n\`\`\`\n${quoted}\n\`\`\`\n`;

    expect(sourceEmailBody(description)).toBe(quoted);
  });

  // The match ends at the fence that closes the mail, not at the end of the
  // description, so anything written after the block does not lose the mail.
  it('still finds the mail when the description goes on after it', () => {
    expect(sourceEmailBody(`${DESCRIPTION}\n**Note** added later.\n`)).toBe(BODY);
  });
});

describe('the source email of an email-raised event', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    resolveOwners.mockImplementation(async (refs: object[]) => refs.map((ref) => ({ ...ref, found: false })));
  });

  it('is offered only for an event that came from an email', async () => {
    renderSidebar({ subject_type: 'host', subject_name: 'app-01', description: 'disk full', labels: {} });
    await settled();

    expect(screen.queryByTestId('toggle-source-email-btn')).not.toBeInTheDocument();
  });

  it('shows the subject, the sender and the mail itself', async () => {
    renderSidebar(emailRow);
    await settled();
    expect(screen.queryByTestId('sidebar-source-email')).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId('toggle-source-email-btn'));
    const panel = screen.getByTestId('sidebar-source-email');

    expect(within(panel).getByText('Alert TRIGGERED: Critical - Host unreachable')).toBeInTheDocument();
    expect(within(panel).getByText('alert@my.cloud.solarwinds.com')).toBeInTheDocument();
    expect(panel).toHaveTextContent(BODY);
  });

  it('shows the mail rather than the parse summary around it', async () => {
    const panel = await openSourceEmail(emailRow);

    // The summary table is too wide for the sidebar and repeats it.
    expect(within(panel).queryByTestId('markdown')).not.toBeInTheDocument();
    expect(panel).not.toHaveTextContent('Affected');
  });

  it('falls back to the whole description, unrunnable, when it holds no mail', async () => {
    const panel = await openSourceEmail({ ...emailRow, description: 'Raised by a mail in some other shape.' });

    const markdown = within(panel).getByTestId('markdown');
    expect(markdown).toHaveTextContent('Raised by a mail in some other shape.');
    expect(markdown).toHaveAttribute('data-can-run-code', 'false');
  });

  it('does not repeat the mail as a one-line description under Details', async () => {
    renderSidebar(emailRow);
    await settled();
    fireEvent.click(screen.getByTestId('toggle-details-btn'));

    expect(screen.queryByText('Description')).not.toBeInTheDocument();
  });
});
