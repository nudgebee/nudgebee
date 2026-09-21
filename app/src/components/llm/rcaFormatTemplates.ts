/**
 * Named starting points for the RCA Format settings editor.
 *
 * A template is editor seed text, not a saved entity: picking one loads its
 * Markdown into the editor for the user to edit, and Save stores the resulting
 * string opaquely under the account's single `rca_report_format` — exactly as a
 * hand-written format is stored today.
 *
 * The "Nudgebee default" option is deliberately NOT defined here. Its canonical
 * text is the Go constant `DefaultRCAFormat` in
 * `llm/llm-server/agents/agent_events_report.go`, delivered to the client via the
 * `default_format` field on the `ai_get_rcaformat` response — one source of
 * truth. The heading vocabulary below (emoji-prefixed headings, `---` dividers,
 * bracketed `[placeholder]` guidance, a 5-Whys causality block) is kept
 * consistent with that constant.
 */

export interface RcaFormatTemplate {
  /** Stable key — Select option value and test anchor. */
  id: string;
  /** Picker label. */
  name: string;
  /** One sentence: who it is for / what it optimises for. */
  description: string;
  /** Full Markdown skeleton loaded into the editor. */
  body: string;
}

/** Sentinel id for the built-in template served via `default_format`. */
export const NUDGEBEE_DEFAULT_TEMPLATE_ID = 'nudgebee-default';

const INCIDENT_POSTMORTEM_BODY = `# 📝 Incident Postmortem

## 🧾 Incident Overview
- **Incident ID / Title:** [Short name]
- **Severity:** [SEV1 / SEV2 / SEV3]
- **Status:** [Mitigated / Resolved / Monitoring]
- **Duration:** [Start time] → [End time] ([total minutes])
- **Affected systems:** [Services, components, regions]
- **Customer impact:** [Who was affected and how — error rate, downtime, data]

---

## 📊 Impact Summary
Provide a concise, quantified description of the impact:
- [Requests failed / users affected / SLA or revenue impact]
- [Was data lost or corrupted? Yes/No — detail]

---

## ⏱️ Timeline of Events
All times in [UTC]. Replace these rows with actual data points from the investigation.
- **[Timestamp]** — [Triggering change or first anomaly]
- **[Timestamp]** — [First alert fired / first human detection]
- **[Timestamp]** — [Investigation milestone or escalation]
- **[Timestamp]** — [Mitigation applied]
- **[Timestamp]** — [Full recovery confirmed]

---

## 🔎 Root Cause Analysis

### Primary Cause
[The technical root cause — the triggering event or condition, and how the system responded.]

### ❓ Causality Chain (5-Whys)
- **Symptom:** [The primary issue observed]
- **Why?** [Immediate cause]
- **Why?** [Deeper cause]
- **Why?** [Deeper cause]
- **Why?** [Deeper cause]
- **Root Cause:** [The foundational reason]

### Contributing Factors
1. **[Factor name]** — [How it contributed, with supporting evidence]
2. **[Factor name]** — [How it contributed, with supporting evidence]

---

## 🛠️ Detection & Response
- **How was it detected?** [Alert / customer report / manual] — [was this fast enough?]
- **What slowed the response?** [Missing runbook, unclear ownership, noisy alerts]
- **What went well?** [Things to keep doing]

---

## ✅ Action Items
Each item has an owner and a due date, and is tracked to completion.

| # | Action | Type (Prevent / Detect / Mitigate) | Owner | Due |
|---|--------|------------------------------------|-------|-----|
| 1 | [Action] | [Type] | [Owner] | [Date] |
| 2 | [Action] | [Type] | [Owner] | [Date] |

---

## 🔁 Follow-ups & Lessons Learned
- [Broader systemic risk this incident exposed]
- [Related areas to audit for the same class of failure]
- [Documentation, training, or process changes]

---

## 📋 Notes
[Optional — links to dashboards, logs, chat transcript, related incidents.]`;

const EXECUTIVE_SUMMARY_BODY = `# 📝 Executive Incident Summary

> One page. Lead with impact and resolution. Keep technical detail minimal — link out for depth.

## 🎯 What Happened
[2–3 plain-language sentences: what broke, who noticed, and when.]

---

## 📊 Business Impact
- **Customers affected:** [Number / segment / "none externally visible"]
- **Duration of impact:** [Start] → [End] ([total])
- **Financial / SLA impact:** [Estimate, or "within error budget"]
- **Data integrity:** [No data lost / detail]

---

## ✅ Current Status
[Resolved / Mitigated and monitoring / Ongoing] — [one sentence on stability now.]

---

## 🔑 Root Cause (in brief)
[One or two sentences. No stack traces, no config diffs — the "what", not the "how".]

---

## 🛡️ What We Are Doing About It
- **Immediate:** [Action already taken or in progress] — [owner]
- **Preventive:** [Top 1–3 durable fixes] — [owner, target date]

---

## 🗣️ Customer Communication
[What was said, on which channel, and whether follow-up is required.]

---
*Full technical postmortem: [link]*`;

const ITIL_PROBLEM_RECORD_BODY = `# 📝 Problem Record (ITIL)

## 🆔 Problem Details
- **Problem ID:** [PRB#####]
- **Linked Incidents:** [INC#####, INC#####]
- **Date raised:** [Date]
- **Priority / Impact / Urgency:** [P? / High–Med–Low / High–Med–Low]
- **Problem Owner:** [Name / team]
- **Status:** [Under investigation / Known error / Resolved / Closed]

---

## 📄 Problem Statement
[A clear statement of the underlying problem — the cause of one or more incidents. Describe the deviation from expected service behaviour, the affected service(s), and the observed symptoms.]

---

## 🔎 Root Cause Analysis

### Investigation Summary
[What was examined — logs, metrics, configuration, recent changes — and what it showed.]

### ❓ Causality Chain (5-Whys)
- **Symptom:** [Observed incident symptom]
- **Why?** [Immediate cause]
- **Why?** [Deeper cause]
- **Why?** [Deeper cause]
- **Root Cause:** [Foundational cause]

### Contributing Factors
1. **[Factor]** — [Supporting evidence]

---

## ⚠️ Known Error
- **Known error description:** [The confirmed faulty component or behaviour, once the root cause is understood.]
- **Trigger conditions:** [What makes the error manifest.]
- **Added to Known Error Database:** [Yes/No — reference]

---

## 🩹 Workaround
- **Description:** [Steps to restore service or avoid the error while the permanent fix is pending.]
- **Effectiveness:** [Full / partial — residual risk]
- **Applies to:** [Which incidents / conditions]
- **Communicated to:** [Service desk / affected teams]

---

## 🛠️ Permanent Fix
- **Proposed resolution:** [The change that eliminates the root cause.]
- **Change record:** [CHG##### — link]
- **Risk & rollback:** [Deployment risk and how to revert]
- **Target date:** [Date] — **Owner:** [Name]

---

## ✅ Closure Criteria
- [ ] Permanent fix deployed and verified
- [ ] Linked incidents resolved
- [ ] Known Error Database updated / retired
- [ ] Stakeholders notified

---

## 📋 Notes
[Optional — references, related problems, post-implementation review date.]`;

export const NAMED_RCA_FORMAT_TEMPLATES: RcaFormatTemplate[] = [
  {
    id: 'incident-postmortem',
    name: 'Incident postmortem',
    description: 'Timeline-heavy, with tracked action items and follow-ups.',
    body: INCIDENT_POSTMORTEM_BODY,
  },
  {
    id: 'executive-summary',
    name: 'Executive summary',
    description: 'One page, impact and resolution first, minimal technical detail.',
    body: EXECUTIVE_SUMMARY_BODY,
  },
  {
    id: 'itil-problem-record',
    name: 'ITIL-style problem record',
    description: 'Problem statement, known error, workaround, permanent fix.',
    body: ITIL_PROBLEM_RECORD_BODY,
  },
];
