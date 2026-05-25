import React from 'react';
import { render } from '@testing-library/react';
import SummarySkeletonLoader from '@components1/common/SummarySkeletonLoader';

describe('SummarySkeletonLoader', () => {
  test('renders without crashing', () => {
    const { container } = render(<SummarySkeletonLoader />);
    expect(container).toBeTruthy();
  });

  test('renders skeleton elements', () => {
    const { container } = render(<SummarySkeletonLoader />);
    // MUI Skeleton renders span elements with MuiSkeleton class
    const skeletons = container.querySelectorAll('.MuiSkeleton-root');
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
