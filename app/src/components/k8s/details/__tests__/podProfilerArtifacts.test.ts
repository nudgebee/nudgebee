import zlib from 'zlib';
import { parse } from 'graphql';
import { gqlStringify } from '@lib/HttpService';
import {
  AUTO_DETECT_LANGUAGE,
  artifactsFromAgentResponse,
  artifactsFromStoredProfile,
  buildProfileRequest,
  decodeProfile,
  itemGroupsFromFindings,
  languageForFramework,
  looksLikeSvg,
  reportedLanguage,
} from '../podProfilerArtifacts';

const gzBase64 = (text: string) => zlib.gzipSync(Buffer.from(text, 'utf8')).toString('base64');

// Heads of the artefacts the profiler images actually produce.
const PY_SPY_SVG =
  '<?xml version="1.0" standalone="no"?><!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" "http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd"><svg version="1.1" width="1200" height="406"></svg>';
const PERF_FLAMEGRAPH_SVG =
  '<?xml version="1.0" standalone="no"?>\n<svg version="1.1" width="1200" height="550" xmlns="http://www.w3.org/2000/svg"></svg>';
const AUSTIN_RAW = 'P42;T42:7f3a;/app/main.py:<module>:12;/app/worker.py:leak:30 4096\nP42;T42:7f3a;/app/main.py:<module>:12 128';

const baseRequest = { accountId: 'acc-1', podName: 'web-0', namespace: 'shop', durationSeconds: 15 };

// The agent's own file block, as the current agent answers a profile task.
const agentResponse = (filename: string, contents: string, lang = 'python', profileTool = 'pyspy') => ({
  success: true,
  task_processing_duration: 42,
  data: { filename, contents_base64: contents, lang, profile_tool: profileTool, profile_type: 'cpu', profile_duration: 15 },
});

describe('languageForFramework', () => {
  it.each([
    [undefined, AUTO_DETECT_LANGUAGE],
    ['', AUTO_DETECT_LANGUAGE],
    ['nginx', AUTO_DETECT_LANGUAGE],
    ['envoy', AUTO_DETECT_LANGUAGE],
    ['go', 'go'],
    ['golang', 'go'],
    ['nodejs', 'node'],
    ['Python', 'python'],
    ['java', 'java'],
    ['ruby', 'ruby'],
  ])('maps %p to %p', (framework, expected) => {
    expect(languageForFramework(framework)).toBe(expected);
  });
});

describe('reportedLanguage', () => {
  it.each([
    ['nodejs', 'node'],
    ['golang', 'go'],
    ['Python', 'python'],
    ['go', 'go'],
    // Not one of the Language options: kept as reported, never "auto".
    ['rust', 'rust'],
    ['php', 'php'],
    ['', ''],
    [undefined, ''],
    [42, ''],
  ])('maps %p to %p', (lang, expected) => {
    expect(reportedLanguage(lang)).toBe(expected);
  });
});

describe('buildProfileRequest', () => {
  it.each([
    ['auto', 'cpu', { profile_type: 'cpu' }],
    ['auto', 'memory', { profile_type: 'memory' }],
    ['python', 'cpu', { application_language: 'python', profile_type: 'cpu' }],
    ['python', 'memory', { application_language: 'python', profile_type: 'memory' }],
    ['go', 'cpu', { application_language: 'go', profile_type: 'cpu' }],
    ['go', 'memory', { application_language: 'go', profile_type: 'memory' }],
    ['node', 'cpu', { application_language: 'node', profile_type: 'cpu' }],
    ['node', 'memory', { application_language: 'node', profile_type: 'memory' }],
    ['java', 'jfr', { application_language: 'java', output_type: 'jfr' }],
    ['java', 'heaphistogram', { application_language: 'java', output_type: 'heaphistogram' }],
    ['ruby', 'flamegraph', { application_language: 'ruby', output_type: 'flamegraph' }],
  ])('%s / %s sends exactly the fields that pick the right tool', (language, selection, expected) => {
    const request = buildProfileRequest({ ...baseRequest, language, selection });

    expect(request).toEqual({ account_id: 'acc-1', pod_name: 'web-0', namespace: 'shop', profile_duration: 15, ...expected });
  });

  // The request is inlined into the GraphQL document; a key set to undefined
  // would come out as the bare word `undefined`.
  it('produces a mutation whose request carries no output_type or language for auto-detect', () => {
    const request = buildProfileRequest({ ...baseRequest, language: AUTO_DETECT_LANGUAGE, selection: 'memory' });
    const mutation = `mutation ApplicationProfile { applications_execute_profile(request: ${gqlStringify(request)}) { data { status } } }`;

    expect(mutation).not.toContain('undefined');
    expect(mutation).not.toContain('output_type');
    expect(mutation).not.toContain('application_language');
    expect(() => parse(mutation)).not.toThrow();
  });
});

describe('artifactsFromAgentResponse', () => {
  it('reads the file block the current agent returns, with what it detected', () => {
    const artifacts = artifactsFromAgentResponse(agentResponse('/tmp/agent-flamegraph-7-1.svg.gz', 'QUJD'));

    expect(artifacts).toEqual({
      groups: [[{ type: 'gz', filename: 'agent-flamegraph-7-1.svg.gz', data: 'QUJD' }]],
      lang: 'python',
      profileTool: 'pyspy',
    });
  });

  it('reads the findings older agents return', () => {
    const block = { type: 'gz', filename: 'agent-flamegraph-7-1.svg.gz', data: "b'QUJD'" };
    const artifacts = artifactsFromAgentResponse({ success: true, findings: [{ evidence: [{ data: JSON.stringify([block]) }] }] });

    expect(artifacts).toEqual({ groups: [[block]] });
  });

  it('returns nothing for a response without an artefact', () => {
    expect(artifactsFromAgentResponse({ success: true })).toEqual({ groups: [] });
    expect(artifactsFromAgentResponse(undefined)).toEqual({ groups: [] });
  });
});

describe('artifactsFromStoredProfile', () => {
  it('reads history entries stored as file blocks', () => {
    const block = { type: 'gz', filename: 'agent-raw-1-1.txt.gz', data: 'QUJD', additional_info: { title: 'Pod Profiler' } };

    expect(artifactsFromStoredProfile([block])).toEqual({ groups: [[block]] });
  });

  it('reads history entries stored as the agent response', () => {
    const artifacts = artifactsFromStoredProfile([agentResponse('/tmp/agent-heapdump-9-1.pprof.gz', 'QUJD', 'go', 'pprof')]);

    expect(artifacts.groups).toEqual([[{ type: 'gz', filename: 'agent-heapdump-9-1.pprof.gz', data: 'QUJD' }]]);
    expect(artifacts.lang).toBe('go');
  });
});

describe('looksLikeSvg', () => {
  it.each([PY_SPY_SVG, PERF_FLAMEGRAPH_SVG, '  \n<svg xmlns="http://www.w3.org/2000/svg"/>', '<!DOCTYPE svg><svg/>'])(
    'accepts SVG markup',
    (text) => {
      expect(looksLikeSvg(text)).toBe(true);
    }
  );

  it.each([AUSTIN_RAW, '', 'Thread dump 2026-01-01'])('rejects plain text', (text) => {
    expect(looksLikeSvg(text)).toBe(false);
  });

  // Called from untyped code with whatever a stored profile decoded to.
  it.each([undefined, null, 42, {}, ['<svg/>']])('rejects a non-string (%p) instead of throwing', (value) => {
    expect(looksLikeSvg(value)).toBe(false);
  });
});

describe('decodeProfile', () => {
  const decodeAgentFile = (filename: string, contents: string) =>
    decodeProfile(artifactsFromAgentResponse(agentResponse(filename, contents)).groups, 'web-0');

  it('renders a py-spy flamegraph as SVG', async () => {
    const decoded = await decodeAgentFile('/tmp/agent-flamegraph-7-1.svg.gz', gzBase64(PY_SPY_SVG));

    expect(decoded.svgData).toEqual([{ fileName: 'web-0.svg', data: PY_SPY_SVG }]);
    expect(decoded.txtData).toEqual([]);
  });

  it('renders a perf flamegraph as SVG', async () => {
    const decoded = await decodeAgentFile('/tmp/agent-flamegraph-7-1.svg.gz', gzBase64(PERF_FLAMEGRAPH_SVG));

    expect(decoded.svgData).toEqual([{ fileName: 'web-0.svg', data: PERF_FLAMEGRAPH_SVG }]);
  });

  it('renders a raw memory profile mislabelled .svg.gz as text', async () => {
    const decoded = await decodeAgentFile('/tmp/agent-raw-42-1.svg.gz', gzBase64(AUSTIN_RAW));

    expect(decoded.svgData).toEqual([]);
    expect(decoded.txtData).toEqual([{ fileName: 'agent-raw-42-1.txt', data: AUSTIN_RAW }]);
    expect(decoded.fileExt).toBe('txt');
  });

  it('renders a raw memory profile named .txt.gz as text', async () => {
    const decoded = await decodeAgentFile('/tmp/agent-raw-42-1.txt.gz', gzBase64(AUSTIN_RAW));

    expect(decoded.txtData).toEqual([{ fileName: 'agent-raw-42-1.txt', data: AUSTIN_RAW }]);
  });

  it.each(['/tmp/agent-pprof-5-1.pprof.gz', '/tmp/agent-heapdump-5-1.pprof.gz'])('hands a Go profile (%s) on for conversion', async (filename) => {
    const contents = gzBase64('pprof-bytes');
    const decoded = await decodeAgentFile(filename, contents);

    expect(decoded.pprofData).toBe(contents);
    expect(decoded.fileExt).toBe('pprof.gz');
    expect(decoded.svgData).toEqual([]);
  });

  it('keeps multi-byte UTF-8 intact in an uncompressed SVG block', async () => {
    const svg = '<svg><text>naïve_größe 処理 (app/ü.py:3)</text></svg>';
    const groups = [[{ type: 'svg', filename: 'py-spy.svg', data: `b'${Buffer.from(svg, 'utf8').toString('base64')}'` }]];
    const decoded = await decodeProfile(groups, 'web-0');

    expect(decoded.svgData).toEqual([{ fileName: 'py-spy.svg', data: svg }]);
  });

  it('keeps multi-byte UTF-8 intact in a compressed text profile', async () => {
    const raw = 'P42;T42:7f3a;/app/données.py:charger:12 4096';
    const decoded = await decodeAgentFile('/tmp/agent-raw-42-1.svg.gz', gzBase64(raw));

    expect(decoded.txtData).toEqual([{ fileName: 'agent-raw-42-1.txt', data: raw }]);
  });

  it('keeps decoding the python-bytes wrapped blocks older agents send', async () => {
    const groups = itemGroupsFromFindings([
      { evidence: [{ data: JSON.stringify([{ type: 'gz', filename: 'agent-heaphistogram-3-1.txt.gz', data: `b'${gzBase64('histogram')}'` }]) }] },
    ]);
    const decoded = await decodeProfile(groups, 'web-0');

    expect(decoded.txtData).toEqual([{ fileName: 'agent-heaphistogram-3-1.txt', data: 'histogram' }]);
  });
});
