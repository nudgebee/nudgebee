import { useRouter } from 'next/router';

function RedirectPage(): void {
  const router = useRouter();
  if (typeof window !== 'undefined') {
    router.push('/home');
  }
}

export default RedirectPage;
