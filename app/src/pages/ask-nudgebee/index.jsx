import KubernetesLLMResponseGenerator from '@components/llm/KubernetesLLMResponseGeneratorV2';
import PermissionDeniedContent from '@components/common/navigation/PermissionDeniedContent';
import { useRouter } from 'next/router';
import { useMemo, memo } from 'react';
import { withAccountGuard } from '@shared/AccountGuard';
import { hasPermission, isGrantsOnlyUser, missingPermissionMessage } from '@lib/auth';

const AskNudgebee = memo(() => {
  const router = useRouter();

  // Memoize accountId to prevent unnecessary re-renders
  const accountId = useMemo(() => router.query.accountId, [router.query.accountId]);

  return <KubernetesLLMResponseGenerator accountId={accountId} />;
});

AskNudgebee.displayName = 'AskNudgebee';

const GuardedAskNudgebee = withAccountGuard(AskNudgebee, { hideContent: true });

// Nubi is the default landing page (`/`, the rail logo, error pages, invite and
// tenant switch all send users here). A custom-role user whose grants don't
// include `ai_conversations:Read` can't use it — the rail greys the Nubi item
// out for them and the backend refuses its chat reads — so render a
// permission-denied placeholder instead of a page they can't use (same check
// as the rail's gate in components/common/layout/index.jsx; built-in roles
// are unaffected).
const AskNudgebeePage = (props) => {
  const noNubiAccess = isGrantsOnlyUser() && !hasPermission('ai_conversations', 'Read');

  if (noNubiAccess) {
    return <PermissionDeniedContent message={missingPermissionMessage('ai_conversations:Read')} />;
  }
  return <GuardedAskNudgebee {...props} />;
};

export default AskNudgebeePage;
