import React from 'react';
import { Box, Typography } from '@mui/material';
import AddOutlinedIcon from '@mui/icons-material/AddOutlined';
import LibraryBooksOutlinedIcon from '@mui/icons-material/LibraryBooksOutlined';
import UploadFileOutlinedIcon from '@mui/icons-material/UploadFileOutlined';
import AutoAwesomeOutlinedIcon from '@mui/icons-material/AutoAwesomeOutlined';
import { Modal } from '@ui/Modal';
import { Card } from '@ui/Card';
import { Chip } from '@ui/Chip';
import { useTenantBranding } from '@hooks/useTenantBranding';
import { ds } from '@utils/colors';

export type CreateDashboardOption = 'blank' | 'template' | 'generate' | 'import';

interface CreateDashboardOptionsModalProps {
  open: boolean;
  onClose: () => void;
  onSelect: (option: CreateDashboardOption) => void;
}

interface OptionCardProps {
  id: string;
  icon: React.ReactNode;
  title: string;
  description: string;
  badge?: string;
  onClick: () => void;
}

const OptionCard: React.FC<OptionCardProps> = ({ id, icon, title, description, badge, onClick }) => (
  <Card interactive elevation='flat' variant='outlined' size='lg' onClick={onClick} id={id} data-testid={id}>
    <Box
      sx={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        height: ds.space.mul(7, 3),
        borderRadius: ds.radius.lg,
        background: `radial-gradient(circle, ${ds.background[100]} 0%, ${ds.blue[100]} 100%)`,
        color: ds.purple[400],
        mb: ds.space[4],
        '& svg': { fontSize: ds.text.display },
      }}
    >
      {icon}
    </Box>
    <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], mb: ds.space[1] }}>
      <Typography sx={{ fontSize: ds.text.title, fontWeight: ds.weight.semibold }}>{title}</Typography>
      {badge && (
        <Chip size='2xs' tone='agent'>
          {badge}
        </Chip>
      )}
    </Box>
    <Typography sx={{ fontSize: ds.text.body, color: ds.gray[500] }}>{description}</Typography>
  </Card>
);

// The Dashboards page's "New dashboard" chooser — the same four-way start the
// Automation page offers. Each option opens its own flow (and its own account
// picker where one is needed), all ending in the same unsaved editor draft.
const CreateDashboardOptionsModal: React.FC<CreateDashboardOptionsModalProps> = ({ open, onClose, onSelect }) => {
  const { assistantName } = useTenantBranding();
  return (
    <Modal
      open={open}
      handleClose={onClose}
      width='md'
      hideTitleBackground
      title='Create a New Dashboard'
      subtitle='Choose how you would like to get started'
    >
      <Box
        sx={{
          display: 'grid',
          gridTemplateColumns: { xs: '1fr', sm: 'repeat(2, 1fr)' },
          gap: ds.space[4],
          p: `${ds.space[2]} ${ds.space[3]} ${ds.space[4]}`,
        }}
      >
        <OptionCard
          id='new-blank-dashboard-btn'
          icon={<AddOutlinedIcon />}
          title='Blank dashboard'
          description='Start with an empty canvas and add panels yourself'
          onClick={() => onSelect('blank')}
        />
        <OptionCard
          id='open-template-gallery-btn'
          icon={<LibraryBooksOutlinedIcon />}
          title='Use a template'
          description='Pre-built dashboards for common services and clusters'
          onClick={() => onSelect('template')}
        />
        <OptionCard
          id='open-generate-dashboard-btn'
          icon={<AutoAwesomeOutlinedIcon />}
          title={`Generate with ${assistantName}`}
          badge='BETA'
          description='Describe what you want to see in plain English'
          onClick={() => onSelect('generate')}
        />
        <OptionCard
          id='open-import-dashboard-btn'
          icon={<UploadFileOutlinedIcon />}
          title='Import dashboard'
          description='Paste or upload a dashboard JSON, including Grafana exports'
          onClick={() => onSelect('import')}
        />
      </Box>
    </Modal>
  );
};

export default CreateDashboardOptionsModal;
