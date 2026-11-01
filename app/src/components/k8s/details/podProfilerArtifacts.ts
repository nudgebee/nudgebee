import zlib from 'zlib';

// Language value meaning "let the agent decide". The request then carries no
// application_language, and the agent resolves the language itself from the
// runtime it sees in the target container.
export const AUTO_DETECT_LANGUAGE = 'auto';

// Languages whose Profile Type is CPU / Memory. For these the request names
// only profile_type and every agent version derives the (tool, output) pair
// from (language, profile_type). An output_type sent alongside makes agents
// skip that mapping and pick the tool from the output type alone, so "Memory"
// silently ran the CPU profiler.
export const PROFILE_TYPE_LANGUAGES: readonly string[] = ['python', 'go', 'node', AUTO_DETECT_LANGUAGE];

// Runtime names the pod page can hand us as `framework` (the container's
// reported application type, with "golang" already shortened to "go"), mapped
// onto the Language options. Anything else (no report, or a native binary such as
// nginx) is left to the agent.
const FRAMEWORK_LANGUAGES: Record<string, string> = {
  go: 'go',
  golang: 'go',
  java: 'java',
  python: 'python',
  node: 'node',
  nodejs: 'node',
  ruby: 'ruby',
};

export const languageForFramework = (framework: unknown): string => {
  if (typeof framework !== 'string') {
    return AUTO_DETECT_LANGUAGE;
  }
  return FRAMEWORK_LANGUAGES[framework.trim().toLowerCase()] ?? AUTO_DETECT_LANGUAGE;
};

// The language an agent reports it ran, in the Language options' terms where
// it is one of them ("nodejs" -> "node"). Anything else is kept as reported:
// it names what actually ran, and must never read as "Auto-detect".
export const reportedLanguage = (lang: unknown): string => {
  if (typeof lang !== 'string') {
    return '';
  }
  return FRAMEWORK_LANGUAGES[lang.trim().toLowerCase()] ?? lang.trim();
};

export type ProfileRequestInput = {
  accountId: string;
  podName: string;
  namespace: string;
  language: string;
  // The Profile Type selection: cpu/memory for PROFILE_TYPE_LANGUAGES, an
  // output type (jfr, threaddump, flamegraph, …) for everything else.
  selection: string;
  durationSeconds: number;
};

export const buildProfileRequest = ({
  accountId,
  podName,
  namespace,
  language,
  selection,
  durationSeconds,
}: ProfileRequestInput): Record<string, string | number> => {
  const request: Record<string, string | number> = {
    account_id: accountId,
    pod_name: podName,
    namespace,
    profile_duration: durationSeconds,
  };
  // Keys are left out rather than set to undefined: the request is inlined
  // into the GraphQL document, where undefined is not a value.
  if (language !== AUTO_DETECT_LANGUAGE) {
    request.application_language = language;
  }
  if (PROFILE_TYPE_LANGUAGES.includes(language)) {
    request.profile_type = selection;
  } else {
    request.output_type = selection;
  }
  return request;
};

// One evidence file block as the profiler screen consumes it.
export type ProfilerItem = {
  type?: string;
  filename?: string;
  data?: string;
};

export type ProfilerArtifacts = {
  // File blocks grouped per evidence, the unit the decoder works on.
  groups: ProfilerItem[][];
  // What the agent reports it actually ran. Only agents that return the bare
  // file block report it; it is the resolved language when none was sent.
  lang?: string;
  profileTool?: string;
};

const isRecord = (value: unknown): value is Record<string, any> => typeof value === 'object' && value !== null && !Array.isArray(value);

const isFileItem = (value: unknown): value is ProfilerItem =>
  isRecord(value) && typeof value.type === 'string' && typeof value.filename === 'string' && typeof value.data === 'string';

const basename = (path: string): string => path.split('/').pop() || path;

// Older agents answer with findings[].evidence[], where each evidence `data`
// is a JSON array of file blocks.
export const itemGroupsFromFindings = (findings: unknown): ProfilerItem[][] => {
  if (!Array.isArray(findings)) {
    return [];
  }
  const groups: ProfilerItem[][] = [];
  for (const finding of findings) {
    const evidences = isRecord(finding) && Array.isArray(finding.evidence) ? finding.evidence : [];
    for (const evidence of evidences) {
      if (!isRecord(evidence) || typeof evidence.data !== 'string') {
        continue;
      }
      try {
        const parsed = JSON.parse(evidence.data);
        if (Array.isArray(parsed)) {
          groups.push(parsed.filter(isFileItem));
        }
      } catch (error) {
        console.error('Error parsing profiler evidence:', error);
      }
    }
  }
  return groups;
};

// Newer agents answer with the file block itself under `data`:
// {filename, contents_base64, lang, profile_tool, …}. Descend through `data`
// wrappers until the block appears, so an extra or a missing layer still reads.
const findAgentFileBlock = (node: unknown): Record<string, any> | null => {
  let current = node;
  for (let depth = 0; depth < 3 && isRecord(current); depth++) {
    if (typeof current.contents_base64 === 'string' && current.contents_base64 !== '' && typeof current.filename === 'string') {
      return current;
    }
    current = current.data;
  }
  return null;
};

const artifactsFromFileBlock = (block: Record<string, any>): ProfilerArtifacts => {
  const filename = basename(block.filename);
  const lastDot = filename.lastIndexOf('.');
  return {
    groups: [
      [
        {
          // The final extension, as file blocks carry it: "gz" for every
          // artefact the profiler produces.
          type: lastDot > 0 ? filename.slice(lastDot + 1) : 'gz',
          filename,
          data: block.contents_base64,
        },
      ],
    ],
    lang: typeof block.lang === 'string' && block.lang ? block.lang : undefined,
    profileTool: typeof block.profile_tool === 'string' && block.profile_tool ? block.profile_tool : undefined,
  };
};

// The agent's response as applications_get_profile_status returns it in
// base64_profile.data — either shape above.
export const artifactsFromAgentResponse = (response: unknown): ProfilerArtifacts => {
  if (!isRecord(response)) {
    return { groups: [] };
  }
  if (Array.isArray(response.findings)) {
    return { groups: itemGroupsFromFindings(response.findings) };
  }
  const block = findAgentFileBlock(response);
  return block ? artifactsFromFileBlock(block) : { groups: [] };
};

// A stored profile-history entry (application_profile.profile, parsed): a list
// holding either the file blocks themselves or the agent's whole response.
export const artifactsFromStoredProfile = (profile: unknown): ProfilerArtifacts => {
  const entries = Array.isArray(profile) ? profile : [profile];
  const blocks = entries.filter(isFileItem);
  if (blocks.length > 0) {
    return { groups: [blocks] };
  }
  for (const entry of entries) {
    const artifacts = artifactsFromAgentResponse(entry);
    if (artifacts.groups.length > 0) {
      return artifacts;
    }
  }
  return { groups: [] };
};

// Whether decoded text is SVG markup. Profiler images used to name raw-text
// profiles (a Python memory profile) "*.svg.gz", so the suffix alone cannot
// decide how to render a file.
export const looksLikeSvg = (text: unknown): boolean => {
  if (typeof text !== 'string') {
    return false;
  }
  const head = text.trim().slice(0, 64).toLowerCase();
  return head.startsWith('<?xml') || head.startsWith('<svg') || head.startsWith('<!doctype svg');
};

const stripPythonBytesWrapper = (data: string): string => data.replace(/^b'|'$/g, '');

export const base64ToBuffer = (data: string): Buffer => Buffer.from(stripPythonBytesWrapper(data), 'base64');

export const unzipToText = (gzData: Buffer): Promise<string> =>
  new Promise((resolve, reject) => {
    zlib.unzip(gzData, (err, unzippedBuffer) => {
      if (err) {
        console.error('Error unzipping the file:', err);
        reject(err);
      } else {
        resolve(unzippedBuffer.toString('utf8').replace(/\n$/, ''));
      }
    });
  });

export const findPySpyCmdReplaceWithPid = (data: string): string => {
  const matches = data.match(/py-spy[^\n]*\.svg/g);
  if (!matches?.length) {
    return data;
  }
  let result = data;
  for (const match of matches) {
    const pidMatch = match.match(/--pid=(\d+)/);
    if (pidMatch?.[1]) {
      result = result.replace(match, 'Process Id: ' + pidMatch[1]);
    }
  }
  return result;
};

export type DecodedFile = {
  fileName: string;
  data: string;
};

export type DecodedProfile = {
  svgData: DecodedFile[];
  txtData: DecodedFile[];
  jfrData: DecodedFile[];
  // Base64 of a gzipped pprof profile; the screen converts it to SVG server-side.
  pprofData: string;
  fileExt: string;
};

const isGzNamed = (item: ProfilerItem, suffix: string) => item.type === 'gz' && (item.filename ?? '').endsWith(suffix);

export const decodeProfile = async (groups: ProfilerItem[][], podName: string): Promise<DecodedProfile> => {
  const decoded: DecodedProfile = { svgData: [], txtData: [], jfrData: [], pprofData: '', fileExt: '' };

  for (const items of groups) {
    try {
      for (const item of items.filter((i) => i.type === 'svg')) {
        decoded.svgData.push({
          fileName: item.filename ?? `${podName}.svg`,
          // atob() yields one character per byte, which mangles multi-byte UTF-8.
          data: findPySpyCmdReplaceWithPid(base64ToBuffer(item.data ?? '').toString('utf8')),
        });
      }

      const svgGzItems = items.filter((i) => isGzNamed(i, 'svg.gz'));
      if (svgGzItems.length) {
        const unzipped = await unzipToText(base64ToBuffer(svgGzItems[0].data ?? ''));
        if (looksLikeSvg(unzipped)) {
          decoded.svgData.push({ fileName: `${podName}.svg`, data: unzipped });
        } else {
          decoded.fileExt = 'txt';
          decoded.txtData.push({
            fileName: basename(svgGzItems[0].filename ?? '').replace(/\.svg\.gz$/, '.txt'),
            data: unzipped,
          });
        }
      }

      const txtGzItems = items.filter((i) => isGzNamed(i, 'txt.gz'));
      if (txtGzItems.length) {
        decoded.fileExt = 'txt';
        for (const item of txtGzItems) {
          decoded.txtData.push({
            fileName: (item.filename ?? '').replace('.gz', ''),
            data: await unzipToText(base64ToBuffer(item.data ?? '')),
          });
        }
      }

      const jfrGzItems = items.filter((i) => isGzNamed(i, 'jfr.gz'));
      if (jfrGzItems.length) {
        decoded.fileExt = 'jfr';
        for (const item of jfrGzItems) {
          decoded.jfrData.push({
            fileName: (item.filename ?? '').replace('.gz', ''),
            data: await unzipToText(base64ToBuffer(item.data ?? '')),
          });
        }
      }

      const pprofGzItems = items.filter((i) => isGzNamed(i, 'pprof.gz'));
      if (pprofGzItems.length) {
        decoded.fileExt = 'pprof.gz';
        decoded.pprofData = stripPythonBytesWrapper(pprofGzItems[0].data ?? '');
      }
    } catch (error) {
      console.error('Error processing evidence data:', error);
    }
  }

  return decoded;
};
