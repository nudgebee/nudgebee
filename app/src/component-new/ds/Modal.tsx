/**
 * Modal — DS rename of legacy Modal (deferred MVP wrapper per Q2).
 * Spec: app/design-system/primitives/overlays/modal.html
 *
 * V1 API preserved verbatim. Per v2 plan §3.3 W9 / §13 Q2, Modal ships as a
 * simple re-export of the existing V1 entry point; combined with NDialog the
 * cluster has 238 importers — a full DS redesign is postponed to a 2027 pass
 * (per D3) and instead this primitive remains a CSS-ONLY refresh.
 */
import { Modal } from '@components1/common/modal';
export { Modal };
export default Modal;
