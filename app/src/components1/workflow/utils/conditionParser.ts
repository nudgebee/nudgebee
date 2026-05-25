export interface ParsedCondition {
  type: 'if-true' | 'if-false' | 'condition';
  label: string;
  color: string;
}

const COLORS = {
  'if-true': '#22c55e', // green
  'if-false': '#ef4444', // red
  condition: '#8b5cf6', // purple for other conditions
  default: 'rgb(192, 192, 192)', // gray
};

/**
 * Parses a condition expression and returns a simplified label and color.
 */
export function parseConditionLabel(condition: string): ParsedCondition {
  if (!condition) {
    return { type: 'condition', label: 'conditional', color: COLORS.condition };
  }

  const trimmed = condition.trim();

  const truePatterns = [/==\s*['"]true['"]/i, /==\s*true\b/i, /!=\s*['"]false['"]/i, /!=\s*false\b/i];
  const falsePatterns = [/==\s*['"]false['"]/i, /==\s*false\b/i, /!=\s*['"]true['"]/i, /!=\s*true\b/i];

  for (const pattern of truePatterns) {
    if (pattern.test(trimmed)) {
      return { type: 'if-true', label: 'if true', color: COLORS['if-true'] };
    }
  }

  for (const pattern of falsePatterns) {
    if (pattern.test(trimmed)) {
      return { type: 'if-false', label: 'if false', color: COLORS['if-false'] };
    }
  }

  return { type: 'condition', label: 'conditional', color: COLORS.condition };
}

export function getConditionColor(condition: string | undefined): string {
  if (!condition) {
    return COLORS.default;
  }
  return parseConditionLabel(condition).color;
}

export function hasConditionalStyling(condition: string | undefined): boolean {
  return !!condition && condition.trim().length > 0;
}
