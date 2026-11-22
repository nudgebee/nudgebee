import fs from 'fs';
import path from 'path';
import { ROLLOUT_ONLY_ANNOTATIONS, isRolloutOnlyKey } from '@components/k8s/investigate/changeRollback';

// The server decides which later changes block a rollback, and it ignores restart markers — the
// annotations Helm and `kubectl rollout restart` stamp to force a rollout. The dialog tags the same
// keys "(restart marker)" and never calls their value "Now". If the server's list grew a key the
// page does not know, the dialog would present an unchecked value as live.
describe('restart markers', () => {
  const repoRoot = path.join(__dirname, '../../../../..');
  const server = fs.readFileSync(path.join(repoRoot, 'api-server/services/event/service.go'), 'utf8');
  const card = fs.readFileSync(path.join(repoRoot, 'app/src/components/k8s/investigate/cards/LastDeploymentCard.js'), 'utf8');

  const declared = /var rolloutMarkerAnnotations = \[\]string\{([^}]*)\}/.exec(server);
  const serverMarkers = declared ? [...declared[1].matchAll(/"([^"]+)"/g)].map((m) => m[1]) : [];

  it('every marker the server ignores is one the page tags', () => {
    expect(serverMarkers.length).toBeGreaterThan(0);
    serverMarkers.forEach((marker) => {
      expect(ROLLOUT_ONLY_ANNOTATIONS).toContain(marker);
      expect(isRolloutOnlyKey(`spec.template.metadata.annotations.${marker}`)).toBe(true);
    });
  });

  // Some changes are recorded as the annotations map whole rather than one key.
  it('both sides read a whole annotations map by its keys', () => {
    expect(server).toMatch(/func isRolloutMarkerRecord[\s\S]*?strings\.HasSuffix\(path, "metadata\.annotations"\)/);
    expect(card).toMatch(/path\.endsWith\('metadata\.annotations'\)/);
  });
});
