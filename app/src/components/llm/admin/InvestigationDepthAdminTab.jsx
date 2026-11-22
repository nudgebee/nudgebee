/**
 * Admin → AI & Tools → Investigation depth (#41806). InvestigationDepthTab is
 * EE-only (app/src/ee/), stripped from the OSS snapshot — same Turbopack
 * dynamic-import constraint as EgressFilterAdminTab.jsx. scripts/oss-patches.sh
 * range-replaces the marker-delimited block below with a () => null stub.
 */
const InvestigationDepthAdminTab = () => null;

export default InvestigationDepthAdminTab;
