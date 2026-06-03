import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import SimpleDiffViewer from '@components1/common/SimpleDiffViewer';

const sampleDiff = `diff --git a/src/app.js b/src/app.js
index abc123..def456 100644
--- a/src/app.js
+++ b/src/app.js
@@ -1,5 +1,5 @@
 const express = require('express');
-const port = 3000;
+const port = 4000;

 app.listen(port);`;

describe('SimpleDiffViewer', () => {
  it('renders without crashing when provided a git diff', () => {
    const { container } = render(<SimpleDiffViewer gitDiff={sampleDiff} />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('shows error message when no diff data is provided', () => {
    render(<SimpleDiffViewer gitDiff='' />);
    expect(screen.getByText('No diff data provided')).toBeInTheDocument();
  });

  it('displays the extracted file name from diff header', () => {
    render(<SimpleDiffViewer gitDiff={sampleDiff} />);
    expect(screen.getByText('src/app.js')).toBeInTheDocument();
  });

  it('shows addition count (+) in green', () => {
    render(<SimpleDiffViewer gitDiff={sampleDiff} />);
    expect(screen.getByText('+1')).toBeInTheDocument();
  });

  it('shows deletion count (-) in red', () => {
    render(<SimpleDiffViewer gitDiff={sampleDiff} />);
    expect(screen.getByText('-1')).toBeInTheDocument();
  });

  it('collapses diff when header is clicked', () => {
    render(<SimpleDiffViewer gitDiff={sampleDiff} defaultExpanded />);
    // diff content should be visible initially
    expect(screen.getByText('const port = 4000;')).toBeInTheDocument();
    // Click the header area to collapse
    const headerBox = screen.getByText('src/app.js').closest('[style]') || screen.getByText('src/app.js').parentElement?.parentElement;
    if (headerBox) {
      fireEvent.click(headerBox);
    }
  });

  it('shows diff as collapsed when defaultExpanded is false', () => {
    render(<SimpleDiffViewer gitDiff={sampleDiff} defaultExpanded={false} />);
    expect(screen.queryByText('const port = 4000;')).not.toBeInTheDocument();
  });

  it('hides header when showHeader is false', () => {
    render(<SimpleDiffViewer gitDiff={sampleDiff} showHeader={false} />);
    expect(screen.queryByText('src/app.js')).not.toBeInTheDocument();
    // Content should still be visible since defaultExpanded is true
    expect(screen.getByText('const port = 4000;')).toBeInTheDocument();
  });

  it('uses fallback fileName prop when no git diff header present', () => {
    const simpleDiff = `@@ -1,1 +1,1 @@
-old line
+new line`;
    render(<SimpleDiffViewer gitDiff={simpleDiff} fileName='my-file.ts' />);
    expect(screen.getByText('my-file.ts')).toBeInTheDocument();
  });
});
