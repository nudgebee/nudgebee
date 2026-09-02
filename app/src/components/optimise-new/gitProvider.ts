export type GitProvider = 'github' | 'gitlab';

const gitHostname = (repoUrl: string): string | null => {
  const trimmed = repoUrl.trim();
  if (!trimmed) return null;

  const scpLike = trimmed.match(/^[^@\s/]+@([^:\s/]+):/);
  if (scpLike) return scpLike[1].toLowerCase();

  try {
    const normalized = /^[a-z][a-z0-9+.-]*:\/\//i.test(trimmed) ? trimmed : `https://${trimmed}`;
    return new URL(normalized).hostname.toLowerCase();
  } catch {
    return null;
  }
};

export const detectGitProvider = (repoUrl: string | undefined): GitProvider | null => {
  if (!repoUrl) return null;

  const hostname = gitHostname(repoUrl);
  if (!hostname) return null;
  if (hostname === 'github.com' || hostname.endsWith('.github.com')) return 'github';
  if (hostname === 'gitlab.com' || hostname.endsWith('.gitlab.com')) return 'gitlab';
  return null;
};
