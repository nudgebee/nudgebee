import KubernetesLLMResponseGenerator from '@components1/llm/KubernetesLLMResponseGeneratorV2';
import { useRouter } from 'next/router';
import { useMemo, memo } from 'react';

const AskNudgebee = memo(() => {
  const router = useRouter();

  // Memoize accountId to prevent unnecessary re-renders
  const accountId = useMemo(() => router.query.accountId, [router.query.accountId]);

  return <KubernetesLLMResponseGenerator accountId={accountId} />;
});

AskNudgebee.displayName = 'AskNudgebee';

export default AskNudgebee;
