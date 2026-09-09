import { buildEventSubjectHref, hasEventSubjectLink } from '../eventSubjectLink';

const ACCOUNT = 'a2a30b02-0f67-42e5-a2ab-c658230fd798';
const WORKLOAD_ID = '2e7a12c7-2652-5d9a-96fe-967cf02e4cfa';
const POD_ID = 'a2840a0b-c86d-5ca7-9d27-8dfa290de2c8';
const POD_NAME = 'payment-processing-worker-66d5fdbb8d-bzj78';

// Shape of the real dev event 10a475f0-…: the "Where" field shows the pod, but
// the event's cloud_resource_id is its owning Deployment.
const podEvent = {
  subject_type: 'pod',
  subject_name: POD_NAME,
  subject_namespace: 'app-09',
  cloud_resource_id: WORKLOAD_ID,
  cloud_account_id: ACCOUNT,
};

const podRow = { id: POD_ID, name: POD_NAME, type: 'Pod' };
const workloadRow = { id: WORKLOAD_ID, name: 'payment-processing-worker', type: 'Deployment' };

describe('buildEventSubjectHref', () => {
  it('opens the subject pod, not the owning workload the event is linked to', () => {
    expect(buildEventSubjectHref(podEvent, [workloadRow, podRow])).toBe(
      `/kubernetes/podDetails/${POD_ID}?PodDetails=${POD_ID}&accountId=${ACCOUNT}#pod-details`
    );
  });

  it('opens the pod even when it is no longer running (the common case)', () => {
    // is_active is not consulted — a crashed pod is exactly what the user clicked.
    expect(buildEventSubjectHref(podEvent, [podRow])).toContain(`/kubernetes/podDetails/${POD_ID}`);
  });

  it('falls back to the linked workload when the pod row is gone', () => {
    expect(buildEventSubjectHref(podEvent, [workloadRow])).toBe(
      `/kubernetes/details/${ACCOUNT}?namespace=app-09&workloadName=payment-processing-worker#kubernetes/applications`
    );
  });

  it('never falls back to a ReplicaSet, which has no page', () => {
    const rsRow = { id: WORKLOAD_ID, name: 'payment-processing-worker-66d5fdbb8d', type: 'ReplicaSet' };
    // subject_owner is deliberately absent from EventRow — the builder must not
    // use it. With only the ReplicaSet resolvable there is no valid target, so
    // the click does nothing rather than landing on a page that does not exist.
    expect(buildEventSubjectHref(podEvent, [rsRow])).toBeNull();
  });

  it('does not navigate when nothing resolved', () => {
    expect(buildEventSubjectHref(podEvent, [])).toBeNull();
    expect(buildEventSubjectHref(podEvent, null)).toBeNull();
  });

  it('ignores a same-named pod row that is not the subject', () => {
    const other = { id: 'other', name: 'some-other-pod', type: 'Pod' };
    expect(buildEventSubjectHref(podEvent, [other, workloadRow])).toContain('/kubernetes/details/');
  });

  it('never emits a relative path (the /investigate move made those 404)', () => {
    [
      buildEventSubjectHref(podEvent, [podRow]),
      buildEventSubjectHref(podEvent, [workloadRow]),
      buildEventSubjectHref(
        { subject_type: 'deployment', subject_name: 'checkout', subject_namespace: 'demo', cloud_account_id: ACCOUNT, aggregation_key: 'Anomaly' },
        []
      ),
    ].forEach((href) => expect(href?.startsWith('/kubernetes/')).toBe(true));
  });

  it('keeps anomaly subjects on the workload route', () => {
    expect(
      buildEventSubjectHref(
        {
          subject_type: 'deployment',
          subject_name: 'checkout',
          subject_namespace: 'demo',
          cloud_account_id: ACCOUNT,
          aggregation_key: 'Anomaly',
        },
        []
      )
    ).toBe(`/kubernetes/details/${ACCOUNT}?namespace=demo&workloadName=checkout#kubernetes/applications`);
  });

  it('encodes names and namespaces', () => {
    const href = buildEventSubjectHref({ ...podEvent, subject_namespace: 'ns/1' }, [{ id: WORKLOAD_ID, name: 'a b', type: 'Deployment' }]);
    expect(href).toContain('namespace=ns%2F1');
    expect(href).toContain('workloadName=a%20b');
  });

  it('falls back to the router account id when the event carries none', () => {
    expect(buildEventSubjectHref({ ...podEvent, cloud_account_id: undefined }, [podRow], ACCOUNT)).toContain(`accountId=${ACCOUNT}`);
  });

  describe('workload subjects', () => {
    // deployment is the 2nd-largest subject type and almost never an anomaly;
    // its cloud_resource_id is that workload itself (5236/5236 name matches).
    const deployEvent = {
      subject_type: 'deployment',
      subject_name: 'checkout',
      subject_namespace: 'demo',
      cloud_resource_id: WORKLOAD_ID,
      cloud_account_id: ACCOUNT,
    };

    it.each([
      ['Deployment', 'deployment'],
      ['StatefulSet', 'statefulset'],
      ['DaemonSet', 'daemonset'],
      ['Job', 'job'],
      ['CronJob', 'cronjob'],
      ['Rollout', 'deployment'],
    ])('opens the %s page for a non-anomaly workload event', (type, subjectType) => {
      const href = buildEventSubjectHref({ ...deployEvent, subject_type: subjectType }, [{ id: WORKLOAD_ID, name: 'checkout', type }]);
      expect(href).toBe(`/kubernetes/details/${ACCOUNT}?namespace=demo&workloadName=checkout#kubernetes/applications`);
    });

    it.each(['ReplicaSet', 'node'])('does not link a %s, which has no workload page', (type) => {
      expect(buildEventSubjectHref(deployEvent, [{ id: WORKLOAD_ID, name: 'checkout', type }])).toBeNull();
    });
  });

  describe('hasEventSubjectLink', () => {
    it('styles a subject as a link when it can resolve', () => {
      expect(hasEventSubjectLink({ subject_type: 'deployment', cloud_resource_id: WORKLOAD_ID })).toBe(true);
      expect(hasEventSubjectLink({ subject_type: 'pod', cloud_resource_id: WORKLOAD_ID })).toBe(true);
      expect(hasEventSubjectLink({ subject_type: 'deployment', aggregation_key: 'Anomaly' })).toBe(true);
    });

    it('leaves an unresolvable subject unstyled', () => {
      expect(hasEventSubjectLink({ subject_type: 'database' })).toBe(false);
      expect(hasEventSubjectLink(null)).toBe(false);
    });
  });

  describe('non-k8s subjects', () => {
    // Every cloud event carries a cloud_resource_id, but none of these resource
    // types has a page this link can reach. They must be neither styled as a
    // link nor navigable — otherwise the click looks live and does nothing.
    const cases: Array<[string, string]> = [
      ['cloudsql_database', 'sqladmin.googleapis.com/Instance'],
      ['ec2-instance', 'compute-instance'],
      ['gke_cluster', 'container.googleapis.com/Cluster'],
      ['loadbalancer', 'compute-instance'],
      ['cloud_run_revision', 'run.googleapis.com/Service'],
      ['queue', 'api-request'],
      ['node', 'node'],
      ['cluster', 'cluster'],
    ];

    it.each(cases)('does not navigate for a %s subject', (subjectType, resourceType) => {
      const row = { subject_type: subjectType, subject_name: 'thing', cloud_resource_id: WORKLOAD_ID, cloud_account_id: ACCOUNT };
      expect(buildEventSubjectHref(row, [{ id: WORKLOAD_ID, name: 'thing', type: resourceType }])).toBeNull();
    });

    it.each(cases)('does not style a %s subject as a link', (subjectType) => {
      expect(hasEventSubjectLink({ subject_type: subjectType, cloud_resource_id: WORKLOAD_ID })).toBe(false);
    });

    it('does not route a non-k8s subject that happens to resolve to a workload kind', () => {
      const row = { subject_type: 'database', subject_name: 'checkout', cloud_resource_id: WORKLOAD_ID, cloud_account_id: ACCOUNT };
      expect(buildEventSubjectHref(row, [{ id: WORKLOAD_ID, name: 'checkout', type: 'Deployment' }])).toBeNull();
    });
  });

  it('returns null when there is nothing to link to', () => {
    expect(buildEventSubjectHref(null, [])).toBeNull();
    expect(buildEventSubjectHref({ subject_type: 'node', cloud_account_id: ACCOUNT }, [])).toBeNull();
    expect(buildEventSubjectHref({ subject_type: 'pod', subject_name: POD_NAME }, [podRow])).toBeNull();
  });
});
