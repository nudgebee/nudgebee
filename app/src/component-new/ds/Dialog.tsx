/**
 * Dialog — DS rename of legacy NDialog (deferred MVP wrapper per Q2).
 * Spec: app/design-system/primitives/overlays/dialog.html
 *
 * V1 API preserved verbatim. Per v2 plan §3.3 W9 / §13 Q2, Dialog ships as a
 * simple re-export; full DS redesign postponed to 2027 (per D3). Decision-shaped
 * surface (confirm/cancel) — distinct from Modal (interrupt) and Inspector
 * (right-anchored persistent panel).
 */
export { default } from '@components1/common/modal/NDialog';
