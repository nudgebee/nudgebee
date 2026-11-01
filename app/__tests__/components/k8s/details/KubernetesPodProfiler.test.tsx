import React from 'react';
import zlib from 'zlib';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import KubernetesPodProfiler from '@components/k8s/details/KubernetesPodProfiler';
import apiKubernetes1 from '@api1/kubernetes1';

jest.mock('@utils/colors');

jest.mock('@api1/kubernetes1', () => ({
  __esModule: true,
  default: {
    applicationProfile: jest.fn(),
    applicationProfileStatus: jest.fn(),
    applicationProfileConvert: jest.fn(),
  },
}));

jest.mock('@components/k8s/common/KubernetesTable', () => ({
  KubernetesPodProfilerHistory: () => null,
}));

jest.mock('@context/NubiGlobalChatContext', () => ({
  useNubiGlobalChat: () => ({ openWithContext: jest.fn() }),
}));

jest.mock('@hooks/useTenantBranding', () => ({
  DEFAULT_TITLE: 'Assistant',
  getNubiIconUrl: () => 'icon.svg',
}));

// A native select stands in for the design-system dropdown, so the test can
// pick a language and profile type the way a user does.
jest.mock('@ui/FilterDropdown', () => ({
  __esModule: true,
  default: ({ label, options = [], value, onSelect, disabled }: any) => (
    <select
      aria-label={label}
      value={value ?? ''}
      disabled={disabled}
      onChange={(e) => onSelect({ target: { value: e.target.value } }, e.target.value)}
    >
      <option value=''>--</option>
      {options.map((o: any) => (
        <option key={o.value} value={o.value}>
          {o.label}
        </option>
      ))}
    </select>
  ),
}));

jest.mock('@shared/Loader', () => ({
  __esModule: true,
  default: () => <div>Loading</div>,
}));

jest.mock('@shared/viewers/SvgRenderer', () => ({
  __esModule: true,
  default: ({ svg }: { svg: string }) => <div data-testid='svg-render'>{svg}</div>,
}));

const api = apiKubernetes1 as unknown as Record<string, jest.Mock>;

const gzBase64 = (text: string) => zlib.gzipSync(Buffer.from(text, 'utf8')).toString('base64');

const PY_SPY_SVG = '<?xml version="1.0" standalone="no"?><svg version="1.1" width="1200"><text>main.py</text></svg>';
const AUSTIN_RAW = 'P42;T42:7f3a;/app/main.py:<module>:12;/app/worker.py:leak:30 4096';
const PPROF_SVG = '<?xml version="1.0" encoding="UTF-8" standalone="no"?><svg><text>runtime.mallocgc</text></svg>';

const query = { pod_name: 'web-0', namespace_name: 'shop' };

// Status response carrying the agent's own file block, the shape current agents return.
const completedWith = (filename: string, contents: string, lang = 'python') => ({
  data: {
    data: {
      applications_get_profile_status: {
        data: {
          status: 'COMPLETED',
          base64_profile: {
            data: {
              success: true,
              data: { filename, contents_base64: contents, lang, profile_tool: 'x', profile_type: 'cpu', profile_duration: 15 },
            },
          },
        },
      },
    },
  },
});

const submitted = { data: { data: { applications_execute_profile: { data: { profile_task_id: 'task-1', status: 'TODO' } } } } };

const choose = (label: string, value: string) => fireEvent.change(screen.getByLabelText(label), { target: { value } });

const submit = () => fireEvent.click(screen.getByRole('button', { name: 'Submit' }));

const lastRequest = () => api.applicationProfile.mock.calls.at(-1)[0];

beforeEach(() => {
  jest.clearAllMocks();
  api.applicationProfile.mockResolvedValue(submitted);
});

describe('KubernetesPodProfiler request', () => {
  it('defaults to auto-detect when the pod has no detected runtime and sends no language or output type', async () => {
    api.applicationProfileStatus.mockResolvedValue(completedWith('/tmp/agent-raw-42-1.svg.gz', gzBase64(AUSTIN_RAW)));
    render(<KubernetesPodProfiler accountId='acc-1' query={query} />);

    expect(screen.getByLabelText('Language')).toHaveValue('auto');
    choose('Profile Type', 'memory');
    submit();

    await waitFor(() => expect(api.applicationProfile).toHaveBeenCalled());
    expect(lastRequest()).toEqual({ account_id: 'acc-1', pod_name: 'web-0', namespace: 'shop', profile_duration: 15, profile_type: 'memory' });
  });

  it('falls back to auto-detect for a runtime with no profiler', () => {
    render(<KubernetesPodProfiler accountId='acc-1' query={{ ...query, framework: 'nginx' }} />);

    expect(screen.getByLabelText('Language')).toHaveValue('auto');
  });

  it.each([
    ['python', 'python'],
    ['go', 'go'],
    ['nodejs', 'node'],
  ])('preselects %s and sends profile_type without output_type', async (framework, language) => {
    render(<KubernetesPodProfiler accountId='acc-1' query={{ ...query, framework }} />);

    expect(screen.getByLabelText('Language')).toHaveValue(language);
    choose('Profile Type', 'memory');
    submit();

    await waitFor(() => expect(api.applicationProfile).toHaveBeenCalled());
    expect(lastRequest()).toEqual({
      account_id: 'acc-1',
      pod_name: 'web-0',
      namespace: 'shop',
      profile_duration: 15,
      application_language: language,
      profile_type: 'memory',
    });
  });

  it('keeps sending the selected output type for java', async () => {
    render(<KubernetesPodProfiler accountId='acc-1' query={{ ...query, framework: 'java' }} />);

    choose('Profile Type', 'jfr');
    submit();

    await waitFor(() => expect(api.applicationProfile).toHaveBeenCalled());
    expect(lastRequest()).toEqual({
      account_id: 'acc-1',
      pod_name: 'web-0',
      namespace: 'shop',
      profile_duration: 15,
      application_language: 'java',
      output_type: 'jfr',
    });
  });

  it('adopts a runtime the pod page detects after the tab opened', () => {
    const { rerender } = render(<KubernetesPodProfiler accountId='acc-1' query={query} />);
    expect(screen.getByLabelText('Language')).toHaveValue('auto');

    rerender(<KubernetesPodProfiler accountId='acc-1' query={{ ...query, framework: 'python' }} />);

    expect(screen.getByLabelText('Language')).toHaveValue('python');
  });

  it('does not override a language picked by hand', () => {
    const { rerender } = render(<KubernetesPodProfiler accountId='acc-1' query={query} />);
    choose('Language', 'java');

    rerender(<KubernetesPodProfiler accountId='acc-1' query={{ ...query, framework: 'python' }} />);

    expect(screen.getByLabelText('Language')).toHaveValue('java');
  });
});

describe('KubernetesPodProfiler rendering', () => {
  const runProfile = async (framework: string | undefined, profileType: string) => {
    render(<KubernetesPodProfiler accountId='acc-1' query={{ ...query, framework }} />);
    choose('Profile Type', profileType);
    submit();
    await waitFor(() => expect(api.applicationProfileStatus).toHaveBeenCalled());
  };

  it('draws a py-spy flamegraph', async () => {
    api.applicationProfileStatus.mockResolvedValue(completedWith('/tmp/agent-flamegraph-7-1.svg.gz', gzBase64(PY_SPY_SVG)));
    await runProfile('python', 'cpu');

    expect(await screen.findByTestId('svg-render')).toHaveTextContent('main.py');
  });

  it('shows a raw memory profile named .svg.gz as text, not as a broken SVG', async () => {
    api.applicationProfileStatus.mockResolvedValue(completedWith('/tmp/agent-raw-42-1.svg.gz', gzBase64(AUSTIN_RAW)));
    await runProfile('python', 'memory');

    expect(await screen.findByText('File: agent-raw-42-1.txt')).toBeInTheDocument();
    expect(screen.getByText(AUSTIN_RAW)).toBeInTheDocument();
    expect(screen.queryByTestId('svg-render')).not.toBeInTheDocument();
  });

  it('shows a raw memory profile named .txt.gz as text', async () => {
    api.applicationProfileStatus.mockResolvedValue(completedWith('/tmp/agent-raw-42-1.txt.gz', gzBase64(AUSTIN_RAW)));
    await runProfile('python', 'memory');

    expect(await screen.findByText('File: agent-raw-42-1.txt')).toBeInTheDocument();
  });

  it.each([
    ['cpu', '/tmp/agent-pprof-5-1.pprof.gz'],
    ['memory', '/tmp/agent-heapdump-5-1.pprof.gz'],
  ])('converts a Go %s profile and draws it', async (profileType, filename) => {
    const contents = gzBase64('pprof-bytes');
    api.applicationProfileStatus.mockResolvedValue(completedWith(filename, contents, 'go'));
    api.applicationProfileConvert.mockResolvedValue({
      data: { data: { applications_convert_profile: { data: { svg_profile: gzBase64(PPROF_SVG) } } } },
    });
    await runProfile('go', profileType);

    expect(await screen.findByTestId('svg-render')).toHaveTextContent('runtime.mallocgc');
    expect(api.applicationProfileConvert).toHaveBeenCalledWith({ accountId: 'acc-1', base64_profile: contents });
  });

  it('names the language the agent detected', async () => {
    api.applicationProfileStatus.mockResolvedValue(completedWith('/tmp/agent-flamegraph-7-1.svg.gz', gzBase64(PY_SPY_SVG), 'python'));
    await runProfile(undefined, 'cpu');

    expect(await screen.findByText('Detected language: Python')).toBeInTheDocument();
  });

  it.each([
    ['nodejs', 'Detected language: Node'],
    ['golang', 'Detected language: Go'],
    ['php', 'Detected language: php'],
  ])('labels an agent-reported %s as a language, never as Auto-detect', async (lang, label) => {
    api.applicationProfileStatus.mockResolvedValue(completedWith('/tmp/agent-flamegraph-7-1.svg.gz', gzBase64(PY_SPY_SVG), lang));
    await runProfile(undefined, 'cpu');

    expect(await screen.findByText(label)).toBeInTheDocument();
    expect(screen.queryByText('Detected language: Auto-detect')).not.toBeInTheDocument();
  });

  it('still renders the findings older agents return', async () => {
    const block = { type: 'gz', filename: 'agent-flamegraph-7-1.svg.gz', data: `b'${gzBase64(PY_SPY_SVG)}'` };
    api.applicationProfileStatus.mockResolvedValue({
      data: {
        data: {
          applications_get_profile_status: {
            data: { status: 'COMPLETED', base64_profile: { data: { success: true, findings: [{ evidence: [{ data: JSON.stringify([block]) }] }] } } },
          },
        },
      },
    });
    await runProfile('python', 'cpu');

    expect(await screen.findByTestId('svg-render')).toHaveTextContent('main.py');
  });

  it('tells the user to pick a language when an auto-detected profile fails', async () => {
    api.applicationProfileStatus.mockResolvedValue({ data: { data: { applications_get_profile_status: { data: { status: 'FAILED' } } } } });
    await runProfile(undefined, 'cpu');

    expect(await screen.findByText(/select it under Language and submit again/)).toBeInTheDocument();
  });
});
