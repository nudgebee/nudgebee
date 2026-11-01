import React from 'react';
import dayjs from 'dayjs';
import { fireEvent, render, screen } from '@testing-library/react';
import apiKubernetes from '@api1/kubernetes';
import RCACard, { RCAReport } from '@components/k8s/investigate/cards/RCACard';
import { formatClockCompact } from '@shared/format/Datetime';

jest.mock('@api1/kubernetes', () => ({
  __esModule: true,
  default: { generateRCA: jest.fn() },
}));

jest.mock('@shared/viewers/MarkDowns', () => ({
  __esModule: true,
  default: ({ data }) => <pre data-testid='markdown-data'>{data}</pre>,
}));

describe('RCACard retained report rendering', () => {
  const renderFailedReport = async (analysis) => {
    apiKubernetes.generateRCA.mockResolvedValue({
      status: 'FAILED',
      status_reason: 'analysis timed out',
      analysis,
      generated_at: '2026-08-25T12:00:00Z',
    });

    const card = new RCACard();
    await card.canRenderContent(null, { id: 'event-1', cloud_account_id: 'account-1' });
    const Content = card.getContentComponents()[0];
    render(<Content />);

    return screen.getByTestId('markdown-data').textContent;
  };

  it('unwraps a bare fence around the whole report', async () => {
    await expect(renderFailedReport('```\n# Root cause\n```')).resolves.toBe('# Root cause');
  });

  it('unwraps an explicit markdown fence while preserving nested code blocks', async () => {
    const report = '```markdown\n# Root cause\n```go\nfmt.Println("boom")\n```\n```';
    await expect(renderFailedReport(report)).resolves.toBe('# Root cause\n```go\nfmt.Println("boom")\n```');
  });

  it('preserves reports bounded by genuine code blocks', async () => {
    const report = '```go\nfmt.Println("boom")\n```\nThen update the manifest.\n```yaml\nreplicas: 2\n```';
    await expect(renderFailedReport(report)).resolves.toBe(report);
  });

  it('does not render an empty retained-report box for whitespace analysis', async () => {
    apiKubernetes.generateRCA.mockResolvedValue({ status: 'FAILED', analysis: '   ' });
    const card = new RCACard();
    await card.canRenderContent(null, { id: 'event-1', cloud_account_id: 'account-1' });
    const Content = card.getContentComponents()[0];
    render(<Content />);

    expect(screen.queryByTestId('rca-retained-report')).not.toBeInTheDocument();
  });
});

describe('RCA version selection', () => {
  const generated = '2026-09-01T12:00:00Z';
  const earlier = '2026-08-31T12:00:00Z';
  const data = {
    status: 'COMPLETED',
    rca_report_id: 'new',
    analysis: 'new report',
    generated_at: generated,
    rca_versions: [
      { id: 'new', analysis: 'new report', generated_at: generated },
      { id: 'old', analysis: 'old report', generated_at: earlier },
    ],
  };
  it('exports the selected historical report without regenerating it', async () => {
    apiKubernetes.generateRCA.mockClear();
    const regenerate = jest.fn();
    render(<RCAReport data={data} onRegenerate={regenerate} eventId='event' />);
    fireEvent.click(screen.getByLabelText('RCA version'));
    fireEvent.click(screen.getByText(formatClockCompact(earlier)));
    expect(screen.getByTestId('markdown-data')).toHaveTextContent('old report');
    expect(screen.queryByTestId('rca-regenerate-btn')).not.toBeInTheDocument();
    expect(screen.queryByText(/Read only/)).not.toBeInTheDocument();
    expect(screen.getByTitle(dayjs(earlier).format('YYYY-MM-DD HH:mm:ss'))).toHaveTextContent('Generated');
    const writeText = jest.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });
    fireEvent.click(screen.getByTestId('rca-copy-btn'));
    expect(writeText).toHaveBeenCalledWith('old report');
    const createObjectURL = jest.fn().mockReturnValue('blob:report');
    const revokeObjectURL = jest.fn();
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: createObjectURL });
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: revokeObjectURL });
    const click = jest.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
    fireEvent.click(screen.getByTestId('rca-download-btn'));
    expect(createObjectURL).toHaveBeenCalledTimes(1);
    const blob = createObjectURL.mock.calls[0][0];
    const downloaded = await new Promise((resolve) => {
      const reader = new FileReader();
      reader.onload = () => resolve(reader.result);
      reader.readAsText(blob);
    });
    expect(downloaded).toBe('old report');
    expect(click).toHaveBeenCalledTimes(1);
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:report');
    click.mockRestore();
    expect(apiKubernetes.generateRCA).not.toHaveBeenCalled();
    expect(regenerate).not.toHaveBeenCalled();
  });
  it.each([undefined, [], [data.rca_versions[0]]])('keeps the version control visible when history is unavailable (%j)', (rca_versions) => {
    render(<RCAReport data={{ ...data, rca_versions }} />);
    expect(screen.getByLabelText('RCA version')).toBeDisabled();
    expect(screen.getByText('History unavailable')).toBeInTheDocument();
    expect(screen.getByTestId('markdown-data')).toHaveTextContent('new report');
  });
  it('allows selecting a single historical version when the latest report is separate', () => {
    render(<RCAReport data={{ ...data, rca_versions: [data.rca_versions[1]] }} />);
    fireEvent.click(screen.getByLabelText('RCA version'));
    fireEvent.click(screen.getByText(formatClockCompact(earlier)));
    expect(screen.getByTestId('markdown-data')).toHaveTextContent('old report');
  });
  it.each(['IN_PROGRESS', 'FAILED'])('keeps report and generation date during %s', (status) => {
    render(<RCAReport data={{ ...data, status, attempted_at: '2026-09-02T12:00:00Z' }} />);
    expect(screen.getByTestId('markdown-data')).toHaveTextContent('new report');
    expect(screen.getByTitle(dayjs(generated).format('YYYY-MM-DD HH:mm:ss'))).toHaveTextContent('Generated');
  });
});
