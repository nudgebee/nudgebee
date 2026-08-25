import dynamic from 'next/dynamic';
import { Modal } from '@shared/modal';
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const KubernetesLLMResponseGenerator = dynamic<any>(() => import('./KubernetesLLMResponseGeneratorV2'));

interface ConversationPopupProps {
  query: string;
  sessionId: string;
  accountId: string;
  open: boolean;
  handleClose: () => void;
  title?: string;
  source?: string;
  variableNames?: string[];
  variableDefaults?: Record<string, string>;
}

const ConversationPopup: React.FC<ConversationPopupProps> = ({ query, sessionId, accountId, open, handleClose, title = '', source }) => {
  const clearAllAndClose = () => {
    handleClose();
  };

  return (
    <div>
      <Modal
        open={open}
        onClose={clearAllAndClose}
        title={title}
        width='lg'
        sx={{
          '& .MuiDialog-paper': {
            overflowY: 'auto',
            position: 'relative !important',
            minHeight: '90vh',
          },
        }}
      >
        <KubernetesLLMResponseGenerator
          accountId={accountId}
          query={query}
          popup={true}
          sessionId={sessionId}
          source={source}
          // Throwaway test session — must not overwrite the global drawer's pointer.
          persistLastSession={false}
          // variableNames={variableNames as never[] | undefined}
          // variableDefaults={variableDefaults}
        />
      </Modal>
    </div>
  );
};

export default ConversationPopup;
