// Wording and lookups for the document detail view: what the capability catalog says about the
// capabilities a page references, how the user's mark relates to the classifier, and a summary of the
// loaded list. Kept out of the component so it can be tested without rendering.

export interface CapabilityDetail {
  name: string;
  surface?: string | null;
  scope?: string | null;
  description?: string | null;
}

export type LabelTone = 'neutral' | 'info' | 'success' | 'warning' | 'critical';

// narrow = typed, bounded inputs; pass_through = a free-text command or query that can reach a
// whole service (so a step using it is capped at "needs approval"). Anything not classified in
// the catalog counts as unclassified, never as narrow.
export const scopeTone = (scope?: string | null): LabelTone => {
  if (scope === 'narrow') {
    return 'success';
  }
  if (scope === 'pass_through') {
    return 'warning';
  }
  return 'neutral';
};

export const scopeLabel = (scope?: string | null): string => {
  if (scope === 'narrow') {
    return 'Narrow';
  }
  if (scope === 'pass_through') {
    return 'Pass-through';
  }
  return 'Unclassified';
};

export const surfaceLabel = (surface?: string | null): string => {
  if (surface === 'agent_tool') {
    return 'Agent tool';
  }
  if (surface === 'workflow_task') {
    return 'Workflow task';
  }
  return '—';
};

export interface CapabilityRow {
  name: string;
  surface: string;
  scope: string;
  tone: LabelTone;
  description: string;
  inCatalog: boolean;
}

// One row per capability the page references, in the order the page lists them. A name the
// catalog does not know is kept and marked, not dropped: it is exactly what someone checking
// the classifier needs to see.
export const capabilityRows = (names?: string[] | null, details?: CapabilityDetail[] | null): CapabilityRow[] =>
  (names ?? []).map((name) => {
    const detail = (details ?? []).find((d) => d.name === name);
    return {
      name,
      surface: surfaceLabel(detail?.surface),
      scope: scopeLabel(detail?.scope),
      tone: scopeTone(detail?.scope),
      description: detail?.description ?? '',
      inCatalog: Boolean(detail),
    };
  });

const KIND_LABEL: Record<string, string> = { sop: 'SOP', knowledge: 'Knowledge' };
const MARK_LABEL: Record<string, string> = { sop: 'SOP', fact: 'Fact' };

// The mark wins whenever it exists; this only says so out loud when the two actually differ.
// A Fact mark and a "knowledge" label (and SOP with SOP) agree, so there is nothing to explain.
export const markVsClassifier = (mark?: string | null, kind?: string | null): string | null => {
  if (!mark || !kind || !KIND_LABEL[kind] || !MARK_LABEL[mark]) {
    return null;
  }
  const agrees = (mark === 'sop' && kind === 'sop') || (mark === 'fact' && kind === 'knowledge');
  if (agrees) {
    return null;
  }
  return `The classifier says ${KIND_LABEL[kind]}; your mark (${MARK_LABEL[mark]}) wins.`;
};

export interface ClassificationCounts {
  sop: number;
  knowledge: number;
  notClassified: number;
}

export const countClassified = (docs: Array<{ kind?: string | null }>): ClassificationCounts => {
  const counts: ClassificationCounts = { sop: 0, knowledge: 0, notClassified: 0 };
  docs.forEach((d) => {
    if (d.kind === 'sop') {
      counts.sop += 1;
    } else if (d.kind === 'knowledge') {
      counts.knowledge += 1;
    } else {
      counts.notClassified += 1;
    }
  });
  return counts;
};
