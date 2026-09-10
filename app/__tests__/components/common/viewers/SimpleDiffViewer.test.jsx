import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import SimpleDiffViewer from '@shared/viewers/SimpleDiffViewer';

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

// A fix that spans several files arrives as one diff string with a "diff --git" header per file.
// Parsed as a single blob, every hunk landed under the first file's name with the counts summed into
// it — an operator approving a PR from the remediation list would have read someone else's changes
// attributed to the wrong file.
describe('SimpleDiffViewer with a multi-file diff', () => {
  const multiFileDiff = `diff --git a/src/one.js b/src/one.js
index aaa111..bbb222 100644
--- a/src/one.js
+++ b/src/one.js
@@ -1,3 +1,3 @@
 const a = 1;
-const b = 2;
+const b = 3;
diff --git a/src/two.js b/src/two.js
index ccc333..ddd444 100644
--- a/src/two.js
+++ b/src/two.js
@@ -10,2 +10,3 @@
 const c = 4;
+const d = 5;`;

  it('names every file in the change, not just the first', () => {
    render(<SimpleDiffViewer gitDiff={multiFileDiff} />);
    expect(screen.getByText('src/one.js')).toBeInTheDocument();
    expect(screen.getByText('src/two.js')).toBeInTheDocument();
  });

  it("counts each file's changes against that file", () => {
    render(<SimpleDiffViewer gitDiff={multiFileDiff} />);
    // one.js: +1/-1. two.js: +1/-0. Summed into one file they would read +2/-1.
    expect(screen.getAllByText('+1')).toHaveLength(2);
    expect(screen.getByText('-1')).toBeInTheDocument();
    expect(screen.getByText('-0')).toBeInTheDocument();
  });

  // A caller that knows one file path passes it as `fileName` (EventRaisePrPanel passes the
  // analysis's file_path). It is the FALLBACK for a section with no "diff --git" header, never an
  // override — otherwise every file in a multi-file diff would render under that one name.
  it('lets each section keep its own name even when a fileName prop is passed', () => {
    render(<SimpleDiffViewer gitDiff={multiFileDiff} fileName='src/one.js' />);
    expect(screen.getByText('src/one.js')).toBeInTheDocument();
    expect(screen.getByText('src/two.js')).toBeInTheDocument();
  });

  it('falls back to the fileName prop only for a section with no header', () => {
    render(<SimpleDiffViewer gitDiff={'@@ -1,2 +1,2 @@\n-a\n+b'} fileName='headerless.txt' />);
    expect(screen.getByText('headerless.txt')).toBeInTheDocument();
  });

  it('keeps a single-file diff on one block', () => {
    render(<SimpleDiffViewer gitDiff={sampleDiff} />);
    expect(screen.getByText('src/app.js')).toBeInTheDocument();
    expect(screen.getAllByText(/^[+-]\d+$/)).toHaveLength(2);
  });
});
