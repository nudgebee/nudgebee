import { useMemo, useState } from 'react';
import PropTypes from 'prop-types';
import { Box, Typography } from '@mui/material';
import { Modal } from '@ui/Modal';
import SearchInput from '@ui/SearchInput';
import SafeIcon from '@shared/icons/SafeIcon';
import { HelpOutlineDarkIcon } from '@assets';
import { TOURS, canAccessTour, useLaunchGuide } from '@components/common/tour';
import { ds } from 'src/utils/colors';

/**
 * Central catalog of in-app guides. Reads the tour registry, lists every tour
 * that opts in (has a `module`), grouped and searchable. Selecting one closes
 * the menu, navigates to the guide's page if needed, and starts it. Adding a
 * guide to the registry makes it appear here automatically — no wiring.
 */
const GuidesMenu = ({ open, onClose }) => {
  const launch = useLaunchGuide();
  const [query, setQuery] = useState('');

  const groups = useMemo(() => {
    const q = query.trim().toLowerCase();
    const visible = Object.values(TOURS).filter((t) => {
      if (!t.module) {
        return false;
      }
      // Hide guides the current user can't complete (e.g. "Add a user" for a
      // non-write account_admin) — the tour would dead-end on a gated button.
      if (!canAccessTour(t)) {
        return false;
      }
      if (!q) {
        return true;
      }
      return `${t.title} ${t.description || ''} ${t.module}`.toLowerCase().includes(q);
    });
    const byModule = {};
    visible.forEach((t) => {
      (byModule[t.module] = byModule[t.module] || []).push(t);
    });
    return Object.entries(byModule);
  }, [query]);

  const handleLaunch = (tourId) => {
    onClose();
    launch(tourId);
  };

  return (
    <Modal open={open} handleClose={onClose} title='Guides' width='sm'>
      <Box sx={{ px: 'var(--ds-space-5)', pb: 'var(--ds-space-4)', display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
        <SearchInput id='guides-search' value={query} onChange={setQuery} onClear={() => setQuery('')} label='Search guides' />

        {groups.length === 0 ? (
          <Typography sx={{ fontSize: ds.text.small, color: ds.gray[400], py: 'var(--ds-space-3)', textAlign: 'center' }}>
            No guides found.
          </Typography>
        ) : (
          groups.map(([module, tours]) => (
            <Box key={module} sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
              <Typography
                sx={{ fontSize: ds.text.caption, fontWeight: 600, letterSpacing: '0.4px', color: ds.gray[400], textTransform: 'uppercase' }}
              >
                {module}
              </Typography>
              {tours.map((tour) => (
                <Box
                  key={tour.id}
                  component='button'
                  type='button'
                  onClick={() => handleLaunch(tour.id)}
                  sx={{
                    display: 'flex',
                    alignItems: 'flex-start',
                    gap: 'var(--ds-space-2)',
                    width: '100%',
                    textAlign: 'left',
                    padding: 'var(--ds-space-2) var(--ds-space-3)',
                    border: `1px solid ${ds.gray[200]}`,
                    borderRadius: 'var(--ds-radius-md)',
                    background: ds.background[100],
                    cursor: 'pointer',
                    transition: 'border-color 0.15s, background 0.15s',
                    '&:hover': { borderColor: 'var(--ds-brand-200)', background: ds.background[200] },
                  }}
                >
                  <Box sx={{ mt: '2px', flexShrink: 0 }}>
                    <SafeIcon src={HelpOutlineDarkIcon} alt='' width={16} height={16} />
                  </Box>
                  <Box sx={{ minWidth: 0 }}>
                    <Typography sx={{ fontSize: ds.text.body, fontWeight: 500, color: ds.gray[700] }}>{tour.title}</Typography>
                    {tour.description && (
                      <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], lineHeight: 1.4 }}>{tour.description}</Typography>
                    )}
                  </Box>
                </Box>
              ))}
            </Box>
          ))
        )}
      </Box>
    </Modal>
  );
};

GuidesMenu.propTypes = {
  open: PropTypes.bool.isRequired,
  onClose: PropTypes.func.isRequired,
};

export default GuidesMenu;
