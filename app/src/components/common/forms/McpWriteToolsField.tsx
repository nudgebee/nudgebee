/**
 * McpWriteToolsField — editor for an MCP integration's `write_tools` field:
 * the server's tools that change something, so Nubi asks for approval before
 * running them (#40399). Every other tool runs without asking.
 *
 * Rendered when a string field carries `widget: "mcp_write_tools"`. The value
 * is the server's own tool names, comma separated, as the form saves list
 * fields. The server's tools are suggested when it can be reached; names can
 * always be typed, so the list stays editable while the server is down.
 */
import * as React from 'react';
import { Box } from '@mui/material';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { Select } from '@ui/Select';
import { Button as DsButton } from '@ui/Button';
import { ds } from '@utils/colors';

interface McpWriteToolsFieldProps {
  value: string;
  onChange: (value: string) => void;
  disabled?: boolean;
  /** The saved integration, to suggest its tools; absent while creating one. */
  integrationId?: string;
  /** An account the integration is linked to, used to list its tools. */
  accountId?: string;
  /** Field label shown above the picker. */
  label?: React.ReactNode;
}

export const parseToolNames = (value: string): string[] =>
  Array.from(
    new Set(
      (value || '')
        .split(',')
        .map((name) => name.trim())
        .filter(Boolean)
    )
  );

export default function McpWriteToolsField({
  value,
  onChange,
  disabled,
  integrationId,
  accountId,
  label = 'Tools that need approval',
}: McpWriteToolsFieldProps) {
  const selected = React.useMemo(() => parseToolNames(value), [value]);
  const [discovered, setDiscovered] = React.useState<string[]>([]);
  const [loading, setLoading] = React.useState(false);

  React.useEffect(() => {
    if (!integrationId || !accountId) return;
    let cancelled = false;
    setLoading(true);
    apiAskNudgebee
      .listTools({ accountId })
      .then((res: any) => {
        const tools: any[] = res?.data?.data?.ai_list_tools?.data ?? [];
        const names = tools
          .filter((tool) => tool?.config?.integration_id === integrationId && tool?.config?.mcp_tool_name)
          .map((tool) => String(tool.config.mcp_tool_name));
        if (!cancelled) setDiscovered(Array.from(new Set(names)).sort());
      })
      .catch(() => {
        if (!cancelled) setDiscovered([]);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [integrationId, accountId]);

  const options = React.useMemo(
    () =>
      Array.from(new Set([...discovered, ...selected])).map((name) => ({
        value: name,
        // Only a list the server returned can say a name is missing from it.
        label: discovered.length === 0 || discovered.includes(name) ? name : `${name} · not offered by the server`,
      })),
    [discovered, selected]
  );
  const save = (names: string[]) => onChange(Array.from(new Set(names)).join(','));

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[2] }} data-testid='mcp-write-tools-field'>
      <Select
        label={label}
        multiple
        value={selected}
        onChange={(names) => save(names as string[])}
        options={options}
        loading={loading}
        disabled={disabled}
        placeholder={discovered.length ? 'Select tools…' : 'Type a tool name…'}
        onCreateOption={(name) => {
          const trimmed = name.trim();
          if (!trimmed) return false;
          save([...selected, trimmed]);
        }}
        instructionText={
          integrationId
            ? 'Names not offered by the server are kept but have no effect until the server offers them again.'
            : 'Save the integration first to choose from its tools, or type tool names now.'
        }
      />
      {discovered.length > 0 && (
        <Box sx={{ display: 'flex', gap: ds.space[2] }}>
          <DsButton
            tone='secondary'
            size='sm'
            disabled={disabled}
            onClick={() => save([...selected, ...discovered])}
            data-testid='mcp-write-tools-mark-all'
          >
            Mark all tools
          </DsButton>
          <DsButton tone='ghost' size='sm' disabled={disabled || selected.length === 0} onClick={() => save([])} data-testid='mcp-write-tools-clear'>
            Clear
          </DsButton>
        </Box>
      )}
    </Box>
  );
}
