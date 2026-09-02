import { detectGitProvider } from '../gitProvider';

describe('detectGitProvider', () => {
  it.each(['https://github.com/owner/repo', 'git@github.com:owner/repo.git', 'ssh://git@github.com/owner/repo.git', 'github.com/owner/repo'])(
    'detects GitHub URL %s',
    (url) => expect(detectGitProvider(url)).toBe('github')
  );

  it.each(['https://gitlab.com/owner/repo', 'git@gitlab.com:owner/repo.git'])('detects GitLab URL %s', (url) =>
    expect(detectGitProvider(url)).toBe('gitlab')
  );

  it.each([
    'https://attackergitlab.com/owner/repo',
    'https://gitlabfake.com/owner/repo',
    'https://gitlab-attacker.com/owner/repo',
    'https://attacker-gitlab.com/owner/repo',
    'https://gitlab.com.attacker.com/owner/repo',
    'https://github.com.attacker.com/owner/repo',
    'https://gitlab-internal.company.com/owner/repo',
    'https://my-gitlab.company.com/owner/repo',
    'not a URL',
    undefined,
  ])('does not classify spoofed or invalid URL %s', (url) => expect(detectGitProvider(url)).toBeNull());
});
