import { renderSlot } from '@lib/slots';

interface LayoutHeaderActionSlotProps {
  open: boolean;
  title: string;
  onClose: () => void;
  buttonTitle?: string;
}

export const LayoutHeaderActionSlot = (props: LayoutHeaderActionSlotProps) => renderSlot('LayoutHeaderAction', props);
