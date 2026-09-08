import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import RCAFormatTab from '@components/llm/RCAFormatTab';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { NAMED_RCA_FORMAT_TEMPLATES } from '@components/llm/rcaFormatTemplates';

// CodeMirror doesn't render in jsdom — swap it for a plain textarea that keeps
// the same value/onChange contract so we can read and type into "the editor".
jest.mock('@uiw/react-codemirror', () => ({
  __esModule: true,
  default: ({ value, onChange }) => <textarea data-testid='rca-editor' value={value} onChange={(e) => onChange(e.target.value)} />,
}));
jest.mock('@codemirror/lang-markdown', () => ({ markdown: () => [] }));
jest.mock('@ui/Toast', () => ({ toast: { success: jest.fn(), error: jest.fn() } }));
jest.mock('@api1/ask-nudgebee', () => ({
  __esModule: true,
  default: { getRcaFormat: jest.fn(), updateRcaFormat: jest.fn() },
}));

const SAVED = 'SAVED FORMAT TEXT';
const NB_DEFAULT = 'NUDGEBEE DEFAULT FORMAT TEXT';
const POSTMORTEM = NAMED_RCA_FORMAT_TEMPLATES[0];

const editor = () => screen.getByTestId('rca-editor');
const saveBtn = () => screen.getByTestId('rca-format-save-btn');
// The DS DropdownMenu renders its panel with a MUI Menu, so while it is open MUI
// marks the rest of the tree aria-hidden — query its items with { hidden: true }.
const picker = () => screen.getByTestId('rca-format-template-picker');

async function pickTemplate(user, label) {
  await user.click(picker());
  const item = await screen.findByRole('menuitem', { name: new RegExp(label), hidden: true });
  await user.click(item);
}

beforeEach(() => {
  jest.clearAllMocks();
  apiAskNudgebee.getRcaFormat.mockResolvedValue({
    data: { format: SAVED, is_default: false, default_format: NB_DEFAULT },
    errors: [],
  });
  apiAskNudgebee.updateRcaFormat.mockResolvedValue({ data: { format: SAVED, is_default: false } });
});

async function renderLoaded() {
  render(<RCAFormatTab accountId='acct-1' />);
  await waitFor(() => expect(editor()).toHaveValue(SAVED));
}

describe('RCAFormatTab template picker', () => {
  it('loads the saved format with Save disabled', async () => {
    await renderLoaded();
    expect(saveBtn()).toBeDisabled();
  });

  it('loads a named template into the editor without a confirm and enables Save', async () => {
    const user = userEvent.setup();
    await renderLoaded();

    await pickTemplate(user, POSTMORTEM.name);

    expect(editor()).toHaveValue(POSTMORTEM.body);
    expect(saveBtn()).toBeEnabled();
    expect(screen.queryByText('Replace editor contents?')).not.toBeInTheDocument();
    expect(apiAskNudgebee.updateRcaFormat).not.toHaveBeenCalled();
  });

  it('loads the Nudgebee default from the API default_format field', async () => {
    const user = userEvent.setup();
    await renderLoaded();

    await pickTemplate(user, 'Nudgebee default');

    expect(editor()).toHaveValue(NB_DEFAULT);
  });

  it('asks before overwriting unsaved edits and only replaces on confirm', async () => {
    const user = userEvent.setup();
    await renderLoaded();

    await user.type(editor(), ' edited');
    expect(editor()).toHaveValue(`${SAVED} edited`);

    await pickTemplate(user, POSTMORTEM.name);

    // Dialog open, editor untouched.
    expect(screen.getByText('Replace editor contents?')).toBeInTheDocument();
    expect(editor()).toHaveValue(`${SAVED} edited`);

    // Cancel keeps the edits.
    await user.click(screen.getByTestId('rca-template-confirm-cancel'));
    await waitFor(() => expect(screen.queryByText('Replace editor contents?')).not.toBeInTheDocument());
    expect(editor()).toHaveValue(`${SAVED} edited`);

    // Pick again and confirm — now it replaces.
    await pickTemplate(user, POSTMORTEM.name);
    await user.click(screen.getByTestId('rca-template-confirm-load'));
    await waitFor(() => expect(editor()).toHaveValue(POSTMORTEM.body));
    expect(saveBtn()).toBeEnabled();
  });

  it('disables the picker until the format has loaded', async () => {
    let resolve;
    apiAskNudgebee.getRcaFormat.mockReturnValue(
      new Promise((r) => {
        resolve = r;
      })
    );
    render(<RCAFormatTab accountId='acct-1' />);

    expect(picker()).toBeDisabled();

    resolve({ data: { format: SAVED, is_default: false, default_format: NB_DEFAULT }, errors: [] });
    await waitFor(() => expect(picker()).toBeEnabled());
  });

  it('keeps the confirm dialog copy scoped to the picked template name', async () => {
    const user = userEvent.setup();
    await renderLoaded();
    await user.type(editor(), ' x');

    await pickTemplate(user, POSTMORTEM.name);
    const dialog = screen.getByText('Replace editor contents?').closest('[role="dialog"]');
    expect(within(dialog).getByText(POSTMORTEM.name)).toBeInTheDocument();
  });
});
