import React from 'react';
import { render } from '@testing-library/react';
import SummarySkeletonLoader from '@shared/SummarySkeletonLoader';

describe('SummarySkeletonLoader', () => {
  test('renders without crashing', () => {
    const { container } = render(<SummarySkeletonLoader />);
    expect(container).toBeTruthy();
  });

  test('renders skeleton elements', () => {
    const { container } = render(<SummarySkeletonLoader />);
    // ds/Skeleton renders each placeholder with role='status' and aria-busy
    const skeletons = container.querySelectorAll('[role="status"][aria-busy="true"]');
    expect(skeletons.length).toBeGreaterThan(0);
  });

  test('renders the grid container', () => {
    const { container } = render(<SummarySkeletonLoader />);
    // The outer Box has display:grid
    const outerBox = container.firstChild;
    expect(outerBox).toBeInTheDocument();
    // There should be 3 child boxes (Service Summary, Utilization & Health, Cost Summary)
    expect(outerBox.children.length).toBe(3);
  });
});
