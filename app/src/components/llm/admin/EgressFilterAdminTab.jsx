/**
 * Admin → AI & Tools → Egress Filter. EgressFilterTab is EE-only
 * (app/src/ee/), stripped from the OSS snapshot — same Turbopack
 * dynamic-import constraint as GatewayAdminTab.jsx. scripts/oss-patches.sh
 * range-replaces the marker-delimited block below (import included, so no
 * import survives unused post-strip) with a () => null stub for the OSS
 * build.
 */
const EgressFilterAdminTab = () => null;

export default EgressFilterAdminTab;
