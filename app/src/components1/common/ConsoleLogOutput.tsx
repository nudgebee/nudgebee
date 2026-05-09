import React, { useMemo } from 'react';

interface ConsoleLogOutputProps {
  data: string;
  sx?: React.CSSProperties;
}

// Regex to detect ANSI colors
const ESC = String.fromCharCode(27);
const RED_ANSI_REGEX = new RegExp(`${ESC}\\[31m`);

// Pre-compile the regex for stripping ANSI codes to avoid re-creation on every call
// Optimization: Move regex creation out of the function scope
const ANSI_REGEX = new RegExp(`${ESC}\\[[0-9;]*m`, 'g');

const stripAnsiCodes = (text: string): string => {
  return text.replace(ANSI_REGEX, '');
};

const ConsoleLogOutput = React.memo(({ data, sx }: ConsoleLogOutputProps) => {
  // Optimization: Memoize split operation to avoid re-splitting large strings on every render
  const lines = useMemo(() => data.split('\n'), [data]);

  const getLineStyles = (line: string): React.CSSProperties => {
    // Optimization: stripAnsiCodes now uses a pre-compiled regex
    const isNoNewerLogsMessage = stripAnsiCodes(line).trim() === 'No newer logs at this moment';

    const baseStyle: React.CSSProperties = {
      padding: '5px 0',
      display: 'flex',
      alignItems: 'center',
    };

    if (isNoNewerLogsMessage) {
      return {
        ...baseStyle,
        fontWeight: 'bold',
        textAlign: 'center' as const,
        display: 'block',
        justifyContent: 'center',
      };
    }

    if (RED_ANSI_REGEX.test(line)) {
      return { ...baseStyle, color: 'red' };
    }

    return baseStyle;
  };

  return (
    <div>
      <pre style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word', ...sx }}>
        {lines.map((line, index) => {
          const cleanLine = stripAnsiCodes(line);
          const isNoNewerLogsMessage = cleanLine.trim() === 'No newer logs at this moment';

          return (
            <div key={index} style={getLineStyles(line)}>
              {!isNoNewerLogsMessage && <span style={{ userSelect: 'none', marginRight: 8, color: 'gray', minWidth: '15px' }}>•</span>}
              <span>{cleanLine}</span>
            </div>
          );
        })}
      </pre>
    </div>
  );
});

// Display name for debugging
ConsoleLogOutput.displayName = 'ConsoleLogOutput';

export default ConsoleLogOutput;
