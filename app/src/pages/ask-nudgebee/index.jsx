import KubernetesLLMResponseGenerator from '@components/llm/KubernetesLLMResponseGeneratorV2';
import { useRouter } from 'next/router';
import { useMemo, memo } from 'react';
import { withAccountGuard } from '@shared/AccountGuard';

const AskNudgebee = memo(() => {
  const router = useRouter();

  // Memoize accountId to prevent unnecessary re-renders
  const accountId = useMemo(() => router.query.accountId, [router.query.accountId]);

  return <KubernetesLLMResponseGenerator accountId={accountId} />;
});

AskNudgebee.displayName = 'AskNudgebee';

export default withAccountGuard(AskNudgebee, { hideContent: true });
