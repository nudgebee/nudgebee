/**
 * Tooltip — DS rename of legacy CustomTooltip (deferred MVP wrapper per Q1).
 * Spec: app/design-system/primitives/overlays/tooltip.html
 *
 * V1 API preserved verbatim. Per v2 plan §3.3 W9 / §13 Q1, Tooltip ships as a
 * simple re-export to complete the library; full V2 design (interactive vs
 * non-interactive split, Popover-based promotion) lands later when the V2_GAPS
 * triage decision (Q1) is signed off.
 */
export { default } from '@components1/common/CustomTooltip';
