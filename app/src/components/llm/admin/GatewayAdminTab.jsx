/**
 * Admin → AI & Tools → Gateway. GatewayConfigTab is EE-only (app/src/ee/),
 * stripped from the OSS snapshot (.oss-exclude) — same Turbopack constraint
 * SettingsModal.jsx's own gateway-config import documents: a dynamic()
 * import of a genuinely-missing module fails at build time, so a runtime
 * .catch() can't rescue it. scripts/oss-patches.sh range-replaces the
 * marker-delimited block below (import included, so no import survives
 * unused post-strip) with a () => null stub for the OSS build.
 */
const GatewayAdminTab = () => null;

export default GatewayAdminTab;
