/**
 * One security finding, normalized for the detail panel.
 *
 * A finding is a join of two objects, and the panel's tabs follow that split:
 *
 *   the vulnerability — description, scoring, weaknesses, references. Shared:
 *     `vulnerabilities` is deduplicated by (source, vuln_id, package, arch), so
 *     the same CVE carries these fields wherever it appears.
 *   this occurrence  — installed version, image layer, namespace, workload or
 *     VM. True of this copy only.
 *
 * The two producers write different payload shapes — image_scan keeps Trivy's
 * PascalCase keys, vm_package_vulnerability writes snake_case — so each gets an
 * adapter here rather than teaching the panel about both.
 */

/** Which scanner produced the finding; picks the "also affects" lookup. */
export type SecurityFindingSource = 'image_scan' | 'vm';

export interface SecurityFinding {
  source: SecurityFindingSource;
  id: string;
  accountId: string;
  vulnId: string;
  title?: string;
  description?: string;
  severity: string;
  status?: string;

  /** Remediation — the one fact that decides whether there is anything to do. */
  installedVersion?: string;
  fixedVersion?: string;
  /** Why there is no fixed version, when the scanner says so ("will_not_fix"). */
  fixState?: string;

  cvssScore?: number;
  cvssVector?: string;
  /** Exploit Prediction Scoring System, 0–1. VM scans only. */
  epss?: number;
  /** In CISA's Known Exploited Vulnerabilities catalogue. VM scans only. */
  kev?: boolean;
  cweIds: string[];
  publishedDate?: string;
  dataSource?: string;
  primaryUrl?: string;
  references: string[];

  packageName?: string;
  packageId?: string;
  packageType?: string;
  image?: string;
  /** Image layer digest that introduced the package. image_scan only. */
  layer?: string;
  namespace?: string;
  workloadName?: string;
  /** VM display name. vm findings only. */
  resourceName?: string;
  resourceId?: string;

  createdAt?: string;
  updatedAt?: string;

  /** Linked ticket / PR resolution, when the row carried them. */
  ticket?: any;
  resolution?: any;
}

/**
 * Canonical severity casing. The scanners already write Capitalized values —
 * `trivySeverity` maps Trivy's UPPERCASE at ingest for both image and CIS scans
 * — but the payload preserves the raw UPPERCASE alongside, and the adapters
 * fall back to it when the column is absent. Normalizing here keeps the label
 * text and its tone consistent whichever source supplied the value.
 */
export const normalizeSeverity = (v: any): string => {
  const s = String(v ?? '').trim();
  if (!s) return 'Unknown';
  return s.charAt(0).toUpperCase() + s.slice(1).toLowerCase();
};

const asArray = (v: any): string[] => {
  if (Array.isArray(v)) return v.filter(Boolean).map(String);
  if (typeof v === 'string' && v.trim()) return [v];
  return [];
};

const asNumber = (v: any): number | undefined => {
  // Number('') and Number('  ') are 0, and 0 is a meaningful score — an absent
  // EPSS must not read as "no chance of exploitation".
  if (v === null || v === undefined || (typeof v === 'string' && v.trim() === '')) return undefined;
  const n = typeof v === 'string' ? Number(v) : v;
  return typeof n === 'number' && Number.isFinite(n) ? n : undefined;
};

/**
 * Trivy reports CVSS as a per-source map ({ nvd: { V3Score, V3Vector }, … }).
 * Prefer nvd, else take the first source that carries a v3 score, so the panel
 * shows a number rather than nothing when only a vendor score exists.
 */
const pickCvss = (cvss: any): { score?: number; vector?: string } => {
  if (!cvss || typeof cvss !== 'object') return {};
  const sources = ['nvd', ...Object.keys(cvss).filter((k) => k !== 'nvd')];
  for (const key of sources) {
    const entry = cvss[key];
    if (!entry || typeof entry !== 'object') continue;
    const score = asNumber(entry.V3Score ?? entry.v3_score);
    if (score !== undefined) return { score, vector: entry.V3Vector ?? entry.v3_vector };
  }
  return {};
};

/** A row from the image-scan findings list (recommendation_security_v2). */
export const fromImageScanRow = (row: any): SecurityFinding => {
  const payload = row?.recommendation || {};
  const cvss = pickCvss(payload.CVSS);
  return {
    source: 'image_scan',
    id: row?.id,
    accountId: row?.account_id,
    vulnId: payload.VulnerabilityID || row?.vulnerability_id || '',
    title: payload.Title,
    description: payload.Description,
    severity: normalizeSeverity(row?.severity || payload.Severity),
    status: row?.status,

    installedVersion: payload.InstalledVersion,
    fixedVersion: payload.FixedVersion,

    cvssScore: cvss.score,
    cvssVector: cvss.vector,
    cweIds: asArray(payload.CweIDs),
    publishedDate: payload.PublishedDate,
    dataSource: payload.DataSource?.Name || payload.DataSource?.ID || payload.SeveritySource,
    primaryUrl: payload.PrimaryURL,
    references: asArray(payload.References),

    packageName: payload.PkgName,
    packageId: payload.PkgID || row?.package_id,
    image: row?.image || payload.image_name,
    layer: payload.Layer?.DiffID || payload.Layer?.Digest,
    namespace: row?.namespace,
    workloadName: row?.workload_name,

    createdAt: row?.created_at,
    updatedAt: row?.updated_at,
    ticket: row?.ticket,
    resolution: row?.resolution,
  };
};

/** A row from the VM findings list (recommendations_list, vm_package_vulnerability). */
export const fromVmRow = (row: any): SecurityFinding => {
  const payload = row?.recommendation || {};
  return {
    source: 'vm',
    id: row?.id,
    accountId: row?.account_id,
    vulnId: payload.vuln_id || '',
    description: payload.description,
    severity: normalizeSeverity(row?.severity),
    status: row?.status,

    installedVersion: payload.package?.version,
    fixedVersion: payload.fixed_version,
    fixState: payload.fix_state,

    cvssScore: asNumber(payload.cvss_v3_score),
    cvssVector: payload.cvss_v3_vector,
    epss: asNumber(payload.epss),
    kev: Boolean(payload.kev),
    cweIds: asArray(payload.cwe_ids),
    dataSource: payload.data_source,
    references: asArray(payload.advisory_ids),

    packageName: payload.package?.name,
    packageType: payload.package?.type,
    resourceName: row?.resource_name,
    resourceId: row?.resource_id,

    createdAt: row?.created_at,
    updatedAt: row?.updated_at,
  };
};

/** Headline for the panel: the CVE id, falling back to the package. */
export const findingHeadline = (f: SecurityFinding) => f.vulnId || f.packageName || 'Finding';

/**
 * Where this occurrence sits, as one line. Image findings are located by
 * namespace/workload, VM findings by the machine.
 */
export const findingLocation = (f: SecurityFinding, accountName?: string) =>
  [accountName, f.source === 'vm' ? f.resourceName : f.namespace, f.source === 'vm' ? undefined : f.workloadName].filter(Boolean).join(' · ');
