import { renderSlot } from '@lib/slots';

interface SignInProviderExtraSlotProps {
  variant: 'v1' | 'v2';
  hasOtherProviders: boolean;
}

export const SignInProviderExtraSlot = (props: SignInProviderExtraSlotProps) => renderSlot('SignInProviderExtra', props);
