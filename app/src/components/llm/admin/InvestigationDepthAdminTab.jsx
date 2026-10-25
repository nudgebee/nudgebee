/**
 * Admin → AI & Tools → Investigation depth (#41806). InvestigationDepthTab is
 * EE-only (app/src/ee/), stripped from the OSS snapshot — same Turbopack
 * dynamic-import constraint as EgressFilterAdminTab.jsx. scripts/oss-patches.sh
 * range-replaces the marker-delimited block below with a () => null stub.
 */
// OSS-STRIP-BEGIN-ADMIN-INVESTIGATION-DEPTH-DYNAMIC-IMPORT
import dynamic from 'next/dynamic';
const InvestigationDepthAdminTab = dynamic(() => import('@ee/components/investigation-depth/InvestigationDepthTab'), { ssr: false });
// OSS-STRIP-END-ADMIN-INVESTIGATION-DEPTH-DYNAMIC-IMPORT

export default InvestigationDepthAdminTab;
