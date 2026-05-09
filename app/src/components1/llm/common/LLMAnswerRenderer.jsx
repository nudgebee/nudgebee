import MarkDowns from '@components1/common/MarkDowns';
import CustomTable from '@components1/common/tables/CustomTable2';
import { getTableDataFromArrayOfObject } from '@lib/util';
import PropTypes from 'prop-types';
import React, { useCallback } from 'react';
import CodeAnalysisRenderer, { isCodeAnalysisShape } from './code-analysis/CodeAnalysisRenderer';

/**
 * Check if parsed JSON is a workflow definition
 * Supports two formats:
 * 1. Standard format: { name, definition: { tasks: [], triggers: [] } }
 * 2. Flat format from LLM: { name, tasks: [], trigger: {} }
 */
const isWorkflowDefinition = (json) => {
  if (!json || typeof json !== 'object') {
    return false;
  }

  // Check for standard format with nested definition
  const hasStandardFormat =
    typeof json.name === 'string' &&
    json.definition &&
    typeof json.definition === 'object' &&
    Array.isArray(json.definition.tasks) &&
    Array.isArray(json.definition.triggers);

  // Check for flat format (LLM may return tasks/trigger at root level)
  const hasFlatFormat = typeof json.name === 'string' && Array.isArray(json.tasks) && (json.trigger || json.triggers);

  return hasStandardFormat || hasFlatFormat;
};

/**
 * Extract the task ID from href like "#task-E1" or "#E1".
 * Returns the ID portion (e.g. "E1") or null.
 */
const extractHrefId = (href) => {
  if (!href) {
    return null;
  }
  const match = href.match(/^#(?:task-)?(.+)$/i);
  return match ? match[1] : null;
};

/**
 * Check if an ID appears as a whole word in text.
 * Prevents "E1" from matching inside "E11".
 */
const matchesWholeWord = (text, word) => {
  if (!text || !word) {
    return false;
  }
  const escaped = word.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const regex = new RegExp(`(?:^|[\\s\\-\\(\\)\\[\\]#])${escaped}(?:$|[\\s\\-\\(\\)\\[\\]#])`, 'i');
  return regex.test(text);
};

/**
 * Try to find a matching tool call message from sibling messages.
 * Uses a multi-pass approach: specific IDs first (plannerId, tool_id),
 * then broader name matches, so precise matches always win.
 */
const matchToolCall = (href, linkText, messages) => {
  if (!messages || messages.length === 0) {
    return null;
  }

  const hrefId = extractHrefId(href);
  const hrefLower = (href || '').toLowerCase();

  const tasks = messages.filter((msg) => {
    const msgType = msg.tool ?? msg.type;
    return msgType !== 'question' && msgType !== 'response';
  });

  // Pass 1: Exact match by plannerId against extracted href ID
  // Handles hyperlinks like [github - E1](#task-E1)
  if (hrefId) {
    for (const msg of tasks) {
      if (msg.plannerId && msg.plannerId.toLowerCase() === hrefId.toLowerCase()) {
        return msg;
      }
    }
  }

  // Pass 2: Whole-word match by plannerId in linkText
  // Handles cases where href doesn't have #task-ID format
  for (const msg of tasks) {
    if (msg.plannerId && matchesWholeWord(linkText, msg.plannerId)) {
      return msg;
    }
  }

  // Pass 3: Match by numeric index (1-based) for #task-1, #task-2, etc.
  // LLM often generates sequential task numbers that correspond to the task order
  if (hrefId && /^\d+$/.test(hrefId)) {
    const numericIndex = Number(hrefId);
    if (numericIndex >= 1 && numericIndex <= tasks.length) {
      return tasks[numericIndex - 1];
    }
  }

  // Pass 4: Match by tool_id or message id (UUIDs)
  for (const msg of tasks) {
    if (msg.tool_id && hrefLower.includes(msg.tool_id.toLowerCase())) {
      return msg;
    }
    if (msg.id && hrefLower.includes(msg.id.toLowerCase())) {
      return msg;
    }
  }

  // Pass 5: Match by tool name or agentName (broad fallback)
  for (const msg of tasks) {
    if (msg.tool) {
      const toolLower = msg.tool.toLowerCase();
      if (matchesWholeWord(linkText, msg.tool) || hrefLower.includes(toolLower)) {
        return msg;
      }
    }
    if (msg.agentName) {
      if (matchesWholeWord(linkText, msg.agentName) || hrefLower.includes(msg.agentName.toLowerCase())) {
        return msg;
      }
    }
  }

  return null;
};

const LLMAnswerRenderer = ({ toolCall, messages = [], onNavigateToTask, groupIndex }) => {
  const handleLinkClick = useCallback(
    (href, linkText) => {
      // Allow normal navigation for external URLs
      if (href && (href.startsWith('http://') || href.startsWith('https://'))) {
        return false;
      }

      // Try to find a matching tool call and navigate to its task card
      const matched = matchToolCall(href, linkText, messages);
      if (matched && onNavigateToTask) {
        onNavigateToTask(groupIndex, matched.originalIndex);
        return true;
      }

      return false;
    },
    [messages, onNavigateToTask, groupIndex]
  );

  const onLinkClickProp = messages.length > 0 ? handleLinkClick : undefined;

  const renderContent = () => {
    const regex = /(^|\n)```markdown\s*\n([\s\S]*?)\n```/i;

    let cleanedText = toolCall.text.replace(/^:/, '').replace(regex, (_, leadingNewline, content) => `${leadingNewline}${content}`);

    try {
      let jsonParsed = JSON.parse(toolCall.text);
      // Unwrap double-encoded JSON (string whose content is itself JSON).
      if (typeof jsonParsed === 'string') {
        const inner = jsonParsed.trim();
        if (inner.startsWith('{') || inner.startsWith('[')) {
          try {
            jsonParsed = JSON.parse(inner);
          } catch {
            // leave as the inner string
          }
        }
      }

      // If JSON.parse returned a primitive (string, number, boolean, null),
      // the agent's output was a quoted literal — not structured data.
      // Rendering a bare string as "structured" iterates its characters into
      // a one-char-per-row table. Skip the structured renderer and fall
      // through to markdown. Arrays/objects are still handled below.
      if (jsonParsed === null || typeof jsonParsed !== 'object') {
        throw new Error('parsed JSON is a primitive; render as markdown');
      }

      // Code analysis (agent_code_2) — render with purpose-built sections instead of a generic table.
      // Sniff on agent name AND payload shape so we don't accidentally match other agents that happen
      // to return a `title` field.
      if (toolCall.agentName === 'agent_code_2' && isCodeAnalysisShape(jsonParsed)) {
        return <CodeAnalysisRenderer data={jsonParsed} onLinkClick={onLinkClickProp} />;
      }

      // Check if this is a workflow definition - render as formatted JSON code block
      // This check MUST come before the table check to prevent workflow JSON from rendering as a table
      if (isWorkflowDefinition(jsonParsed)) {
        const formattedJson = JSON.stringify(jsonParsed, null, 2);
        // Wrap in a styled container with proper code block appearance
        return (
          <pre
            style={{
              backgroundColor: '#1e1e1e',
              color: '#d4d4d4',
              padding: '16px',
              borderRadius: '8px',
              overflow: 'auto',
              fontSize: '13px',
              fontFamily: 'monospace',
              margin: 0,
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-word',
            }}
          >
            <code>{formattedJson}</code>
          </pre>
        );
      }

      if (toolCall.agentName === 'loganalysis' || jsonParsed.stdout || jsonParsed.stderr) {
        const toShowData = jsonParsed.response || jsonParsed.stdout || jsonParsed.stderr;
        return <MarkDowns data={toShowData.replace(/~/g, '\\~')} sx={{ width: '100%', overflowX: 'auto', p: 0 }} onLinkClick={onLinkClickProp} />;
      }
      const { headers, tableData } = getTableDataFromArrayOfObject(jsonParsed);
      if (headers.length > 0) {
        return (
          <CustomTable
            headers={headers}
            tableData={tableData}
            rowsPerPage={tableData.length}
            totalRows={tableData.length}
            renderVertical={tableData.length <= 1}
          />
        );
      }
      // No table fit, but the payload is structured JSON — render it pretty-printed
      // instead of letting it fall through to a single-line markdown blob.
      return (
        <pre
          style={{
            backgroundColor: '#2d3748',
            color: '#E2E8F0',
            padding: '12px',
            borderRadius: '8px',
            overflow: 'auto',
            fontSize: '12px',
            fontFamily: '"Roboto Mono", monospace',
            margin: 0,
            whiteSpace: 'pre-wrap',
            wordBreak: 'break-word',
            lineHeight: 1.6,
          }}
        >
          {JSON.stringify(jsonParsed, null, 2)}
        </pre>
      );
    } catch {
      return (
        <MarkDowns
          data={cleanedText.replace(/~/g, '\\~')}
          sx={{ maxHeight: '100%', width: '100%', overflowX: 'auto', p: 0 }}
          onLinkClick={onLinkClickProp}
        />
      );
    }

    return <MarkDowns data={cleanedText.replace(/~/g, '\\~')} sx={{ width: '100%', overflowX: 'auto', p: 0 }} onLinkClick={onLinkClickProp} />;
  };

  return <>{renderContent()}</>;
};

LLMAnswerRenderer.propTypes = {
  toolCall: PropTypes.shape({
    text: PropTypes.string,
    agentName: PropTypes.string,
    tool: PropTypes.string,
    tool_id: PropTypes.string,
    id: PropTypes.string,
    plannerId: PropTypes.string,
  }).isRequired,
  messages: PropTypes.array,
  onNavigateToTask: PropTypes.func,
  groupIndex: PropTypes.number,
};

export default LLMAnswerRenderer;
