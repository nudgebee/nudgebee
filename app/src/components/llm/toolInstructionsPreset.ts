// Starter for "Add tool instructions": a note mapped to the code agent and marked
// always load, so the code-fix agent gets the account's conventions for a tool
// (Terraform workspaces, Atlas config) on every run. Always-load notes are
// capped at ALWAYS_LOAD_MAX_CHARS, so the template stays well under it.
export const CODE_AGENT_NAME = 'code_analyzer';

export const TOOL_INSTRUCTIONS_PRESET = {
  title: 'Add tool instructions',
  name: '',
  description: 'How the code agent should run this tool in our repositories',
  noteCategory: 'sop',
  content: `## When this applies
Which repositories or directories this covers — e.g. "Repos with a terraform/ directory."

## How to run it
The exact commands and order — e.g. "Run \`terraform workspace select <env>\` before plan or apply."

## Things to avoid
Commands or flags the agent must not use here.
`,
};
