import { parseServiceRepos } from '../VmServiceGit';
import { ANNOTATIONS, serviceAnnotationKey } from '@lib/annotationKeys';

const nginx = '/system.slice/nginx.service';
const api = '/docker/api';

describe('parseServiceRepos', () => {
  it('links two services of one VM to different repos', () => {
    const links = parseServiceRepos([
      { name: serviceAnnotationKey(ANNOTATIONS.WORKLOAD_GIT_REPO, nginx), value: 'https://github.com/org/web' },
      { name: serviceAnnotationKey(ANNOTATIONS.WORKLOAD_GIT_BRANCH, nginx), value: 'release' },
      { name: serviceAnnotationKey(ANNOTATIONS.WORKLOAD_GIT_REPO, api), value: 'https://gitlab.com/org/api' },
    ]);
    expect(links).toEqual({
      [nginx]: { repo: 'https://github.com/org/web', branch: 'release' },
      [api]: { repo: 'https://gitlab.com/org/api', branch: '' },
    });
  });

  it('treats a missing response as no links', () => {
    expect(parseServiceRepos(undefined)).toEqual({});
  });

  it('ignores unrelated attributes and keeps a cleared link empty', () => {
    const links = parseServiceRepos([
      { name: 'framework//docker/api', value: 'nodejs' },
      { name: ANNOTATIONS.WORKLOAD_GIT_REPO, value: 'https://github.com/org/k8s-style' },
      { name: serviceAnnotationKey(ANNOTATIONS.WORKLOAD_GIT_REPO, nginx), value: '' },
    ]);
    expect(links).toEqual({ [nginx]: { repo: '', branch: '' } });
  });
});
