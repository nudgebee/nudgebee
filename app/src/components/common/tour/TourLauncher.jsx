import PropTypes from 'prop-types';
import { Button } from '@ui/Button';
import SafeIcon from '@shared/icons/SafeIcon';
import { HelpOutlineDarkIcon } from '@assets';
import { useLaunchGuide } from './useLaunchGuide';
import { canAccessTour } from './tourAccess';
import { TOURS, brandText } from './tours';

/**
 * Reusable contextual guide launcher — the "How to …" ghost button dropped next
 * to a feature's primary action. Reads the tour from the registry and launches
 * it (navigating first if the tour declares a `route`). One component for every
 * feature; adding a guide never needs a new bespoke button.
 */
const TourLauncher = ({ tourId, label, tone = 'ghost', size = 'md', showIcon = true }) => {
  const launch = useLaunchGuide();
  const tour = TOURS[tourId];
  // Don't offer a guide the user can't complete — even if a caller drops this
  // launcher outside the action's own role gate.
  if (!tour || !canAccessTour(tour)) {
    return null;
  }

  return (
    <Button
      id={`tour-launch-${tourId}`}
      tone={tone}
      size={size}
      icon={showIcon ? <SafeIcon src={HelpOutlineDarkIcon} alt='' width={16} height={16} /> : undefined}
      onClick={() => launch(tourId)}
    >
      {label || `How to: ${brandText(tour.title)}`}
    </Button>
  );
};

TourLauncher.propTypes = {
  tourId: PropTypes.string.isRequired,
  label: PropTypes.string,
  tone: PropTypes.string,
  size: PropTypes.string,
  showIcon: PropTypes.bool,
};

export default TourLauncher;
