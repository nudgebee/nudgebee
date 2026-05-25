import { useRouter } from 'next/router';

export default function AddAccount() {
  const router = useRouter();
  router.push('/user-management#integrations');
  return <> </>;
}
