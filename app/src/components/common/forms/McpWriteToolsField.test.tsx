import { parseToolNames } from './McpWriteToolsField';

describe('parseToolNames', () => {
  it('reads comma-separated tool names, trimmed and de-duplicated', () => {
    expect(parseToolNames(' create_issue, ,delete_issue,create_issue')).toEqual(['create_issue', 'delete_issue']);
  });

  it('treats an empty value as no write tools', () => {
    expect(parseToolNames('')).toEqual([]);
  });
});
