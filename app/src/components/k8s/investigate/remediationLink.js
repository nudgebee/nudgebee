// The investigation report's "Next Steps" and the Remediation tab are the same fix: the tab is their runnable form,
// made automatically. The report says so with one line by its Next Steps heading, added when it is rendered (the
// report's text is not changed).

// A markdown heading line naming the next steps ("### 🧭 Next Steps").
const NEXT_STEPS_HEADING = /^#{1,6}[^\S\n]+[^\n]*\bnext steps\b[^\n]*$/im;

// splitAtNextSteps cuts a report just before its Next Steps heading: {before, after} with the heading starting
// `after`, or null when the report has no such heading.
export function splitAtNextSteps(markdown) {
  if (typeof markdown !== 'string') return null;
  const m = NEXT_STEPS_HEADING.exec(markdown);
  if (!m) return null;
  return { before: markdown.slice(0, m.index), after: markdown.slice(m.index) };
}

// remediationLinkText is the line's text: "{Assistant} made these runnable → Remediation (2 fixes ready)", or, while
// the fix is being prepared, "{Assistant} is preparing a runnable fix → Remediation". '' when there is nothing to say.
export function remediationLinkText(link, assistant = 'The assistant') {
  if (!link) return '';
  const count = Number(link.count) || 0;
  if (count > 0) return `${assistant} made these runnable → Remediation (${count} ${count === 1 ? 'fix' : 'fixes'} ready)`;
  if (link.preparing) return `${assistant} is preparing a runnable fix → Remediation`;
  return '';
}
