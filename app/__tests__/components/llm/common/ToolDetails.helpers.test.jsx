import {
  parseCodeEditParams,
  parseFileViewData,
  diffLanguageForFile,
  lenientUnescape,
  salvageTruncatedParams,
} from '@components/llm/common/ToolDetails';

// Mock heavy internal components — these tests only exercise the exported pure
// helpers, but importing the module pulls the whole component tree.
jest.mock('@shared/viewers/MarkDowns', () => () => null);
jest.mock('@shared/tables/CustomTable', () => () => null);
jest.mock('@components/k8s/common/KubernetesTable', () => () => null);
jest.mock('@components/k8s/common/logTableMapper', () => ({ mapToTableData: () => [] }));
jest.mock('@components/recommendations/security/KubernetesSecurityDetails', () => () => null);
jest.mock('@ui/DiffViewer', () => ({ DiffViewer: () => null }));
jest.mock('@ui/Chart', () => ({ Line: () => null }));

describe('parseCodeEditParams', () => {
  const replaceParams = JSON.stringify({
    file_path: 'api-server/services/account/adapter/code_agent_pr.go',
    old_string: '<<<<<<< HEAD\n\t"strings"\n=======\n>>>>>>> origin/prod\n\t"time"',
    new_string: '\t"strings"\n\t"time"',
    purpose: 'Resolve import conflict by keeping both packages.',
  });

  it('parses a replace tool call into a code edit', () => {
    const edit = parseCodeEditParams({ tool_name: 'replace', parameters: replaceParams });
    expect(edit).not.toBeNull();
    expect(edit.file_path).toBe('api-server/services/account/adapter/code_agent_pr.go');
    expect(edit.new_string).toContain('"time"');
  });

  it('returns null for non-edit tools even with matching params', () => {
    expect(parseCodeEditParams({ tool_name: 'file_view', parameters: replaceParams })).toBeNull();
  });

  it('returns null for edit tools without old/new strings or with broken JSON', () => {
    expect(parseCodeEditParams({ tool_name: 'replace', parameters: '{"file_path":"a.go"}' })).toBeNull();
    expect(parseCodeEditParams({ tool_name: 'replace', parameters: '{"old_string":"a","new_st' })).toBeNull();
    expect(parseCodeEditParams({ tool_name: 'replace', parameters: null })).toBeNull();
    expect(parseCodeEditParams(null)).toBeNull();
  });
});

describe('diffLanguageForFile', () => {
  it.each([
    ['config.yaml', 'yaml'],
    ['data.json', 'json'],
    ['index.tsx', 'javascript'],
    ['run.sh', 'shell'],
    ['README.md', 'markdown'],
    ['query.sql', 'sql'],
    ['main.go', 'text'],
    [undefined, 'text'],
  ])('%s → %s', (file, lang) => {
    expect(diffLanguageForFile(file)).toBe(lang);
  });
});

describe('parseFileViewData', () => {
  const params = JSON.stringify({
    file_path: 'api-server/services/account/adapter/code_agent_pr.go',
    start_line: 150,
    end_line: 152,
    working_directory: '/tmp/code-analysis-x/nudgebee-enterprise',
  });

  it('strips NNN: prefixes and reports the real start line', () => {
    const tc = { tool_name: 'file_view', parameters: params, response: '150: }\n151:\n152: // Create a new context' };
    const out = parseFileViewData(tc);
    expect(out).not.toBeNull();
    expect(out.startLine).toBe(150);
    expect(out.filePath).toBe('api-server/services/account/adapter/code_agent_pr.go');
    expect(out.code).toBe('}\n\n// Create a new context');
  });

  it('returns null for non-file tools and non-numbered output', () => {
    expect(parseFileViewData({ tool_name: 'rg', parameters: params, response: '150: }' })).toBeNull();
    expect(parseFileViewData({ tool_name: 'file_view', parameters: params, response: 'Error: file not found' })).toBeNull();
    expect(parseFileViewData({ tool_name: 'file_view', parameters: params, response: '' })).toBeNull();
    expect(parseFileViewData(null)).toBeNull();
  });

  it('tolerates a broken parameters blob (still renders the code)', () => {
    const out = parseFileViewData({ tool_name: 'file_view', parameters: '{"file_path":"a.go', response: '10: x := 1\n11: y := 2' });
    expect(out).not.toBeNull();
    expect(out.filePath).toBe('');
    expect(out.startLine).toBe(10);
  });
});

describe('salvageTruncatedParams', () => {
  it('returns null when only working memory remains (intention first)', () => {
    const truncated = '{"__intention":"Termination criterion met.","_tool_outputs":{"tool_call_file_view_step_10":"510: display: \'flex\',\\n511:';
    expect(salvageTruncatedParams(truncated)).toBeNull();
  });

  it('returns null when only working memory remains (_tool_outputs first)', () => {
    expect(salvageTruncatedParams('{"_tool_outputs":{"tool_call_rg_step_1":"matched\\n')).toBeNull();
  });

  it('keeps real fields that precede the working-memory blob', () => {
    const out = salvageTruncatedParams('{"answer":"conflict is in \\u003cfile\\u003e","_tool_outputs":{"a":"x');
    expect(out).toContain('"answer":"conflict is in <file>"');
    expect(out).not.toContain('_tool_outputs');
  });

  it('passes unrelated unparseable text through with escapes decoded', () => {
    expect(salvageTruncatedParams('{"query":"a\\nb"')).toBe('{"query":"a\nb"');
  });
});

describe('lenientUnescape', () => {
  it('decodes common JSON escapes in truncated fragments', () => {
    const truncated = '{"_tool_outputs":{"step_10":"510: display: \\u003cflex\\u003e\\n511: gap: 2"';
    const out = lenientUnescape(truncated);
    expect(out).toContain('<flex>');
    expect(out).toContain('\n511:');
    expect(out).not.toContain('\\u003c');
  });

  it('passes plain text through untouched', () => {
    expect(lenientUnescape('no escapes here')).toBe('no escapes here');
    expect(lenientUnescape('')).toBe('');
    expect(lenientUnescape(null)).toBe(null);
  });
});
