// Knowledge Graph settings dialog.
//
// Phase 2 (NB-30989): tabbed shell mirroring `@components1/llm/SettingsModal`.
// - Tab 1 ("Coverage"): cloud account / flow source filter — the legacy
//   single-section UI, extracted into KGCoverageTab.
// - Tab 2 ("Manual Dependencies"): operator-declared CALLS / PUBLISHES_TO /
//   SUBSCRIBES_TO rows, with CSV import + ambiguity resolve.
// - TODO Phase 2.5: 'Diff View' (declared vs auto-detected) and 'Activity'
//   tabs slot in alongside.

import { useState } from 'react';
import { Box } from '@mui/material';
import PropTypes from 'prop-types';
import { Modal } from '@components1/ds/Modal';
import { Tabs } from '@components1/ds/Tabs';
import SafeIcon from '@components1/common/SafeIcon';
import { DataBaseDark, GraphOutlineIcon } from '@assets';
import KGCoverageTab from '@components1/knowledge-graph/KGCoverageTab';
import ManualDependenciesTab from '@components1/knowledge-graph/ManualDependenciesTab';
import { ds } from 'src/utils/colors';

const TAB_COVERAGE = 'coverage';
const TAB_MANUAL_DEPENDENCIES = 'manual-dependencies';

const TABS_CONFIG = [
  { id: TAB_COVERAGE, icon: DataBaseDark, label: 'Coverage', alt: 'coverage', size: 16 },
  { id: TAB_MANUAL_DEPENDENCIES, icon: GraphOutlineIcon, label: 'Manual Dependencies', alt: 'manual-dependencies', size: 16 },
  /* TODO Phase 2.5: { id: 'diff-view', icon: ..., label: 'Diff View',  alt: 'diff-view', size: 16 }, */
  /* TODO Phase 2.5: { id: 'activity',  icon: ..., label: 'Activity',   alt: 'activity',  size: 16 }, */
];

const KGSettings = ({ open, onClose, onSaved }) => {
  const [typeSelected, setTypeSelected] = useState(TAB_COVERAGE);

  const dsTabs = TABS_CONFIG.map((t) => ({
    id: t.id,
    icon: <SafeIcon src={t.icon} alt={t.alt} width={t.size} height={t.size} />,
    label: t.label,
  }));

  return (
    <Modal
      width='lg'
      title='Knowledge Graph'
      open={open}
      handleClose={onClose}
      onClose={onClose}
      // Don't pass `maxHeight` — the legacy Modal converts it into a fixed
      // `height: <value>`, which forces every tab to render full-height even
      // when the form is short (the Coverage tab was leaving ~40% of the
      // dialog empty). Letting the modal size to content lets short tabs
      // shrink and longer tabs (Manual Dependencies with many rows) grow,
      // with the content-area `overflowY: auto` below kicking in if a tab
      // ever exceeds viewport.
      contentStyles={{
        overflowY: 'auto',
        overflowX: 'hidden',
        padding: '0px',
        maxHeight: '85vh',
      }}
    >
      <Box
        sx={{
          position: 'sticky',
          top: 0,
          zIndex: 10,
          backgroundColor: ds?.background?.white ?? '#fff',
          borderBottom: `1px solid ${ds?.border?.secondary ?? '#e5e7eb'}`,
          mb: '16px',
          padding: '0px 24px',
        }}
      >
        <Tabs tabs={dsTabs} value={typeSelected} onChange={(next) => setTypeSelected(next)} size='sm' ariaLabel='Knowledge Graph' />
      </Box>
      <Box sx={{ padding: '0px 24px 24px 24px' }}>
        {typeSelected === TAB_COVERAGE ? (
          <KGCoverageTab open={open} onSaved={onSaved} onClose={onClose} />
        ) : typeSelected === TAB_MANUAL_DEPENDENCIES ? (
          <ManualDependenciesTab open={open} />
        ) : null}
      </Box>
    </Modal>
  );
};

KGSettings.propTypes = {
  open: PropTypes.bool.isRequired,
  onClose: PropTypes.func.isRequired,
  onSaved: PropTypes.func,
};

export default KGSettings;
