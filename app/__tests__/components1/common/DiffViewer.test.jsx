import React from 'react';
import { render } from '@testing-library/react';

// Mock codemirror modules before importing the component
jest.mock('codemirror', () => ({
  basicSetup: {},
  EditorView: {
    lineWrapping: {},
  },
}));

jest.mock('@codemirror/merge', () => ({
  MergeView: jest.fn().mockImplementation(() => ({
    destroy: jest.fn(),
  })),
}));

jest.mock('@codemirror/lang-javascript', () => ({
  javascript: jest.fn(() => ({})),
}));

import CodeMirrorDiffViewer from '@components1/common/DiffViewer';

describe('DiffViewer (CodeMirrorDiffViewer)', () => {
  it('renders without crashing', () => {
    const { container } = render(<CodeMirrorDiffViewer originalCode='const a = 1;' newCode='const a = 2;' />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('renders a container div', () => {
    const { container } = render(<CodeMirrorDiffViewer originalCode='old code' newCode='new code' />);
    expect(container.querySelector('div')).toBeInTheDocument();
  });

  it('creates MergeView with original and new code', () => {
    const { MergeView } = require('@codemirror/merge');
    render(<CodeMirrorDiffViewer originalCode='original content' newCode='new content' />);
    expect(MergeView).toHaveBeenCalledWith(
      expect.objectContaining({
        a: expect.objectContaining({ doc: 'original content' }),
        b: expect.objectContaining({ doc: 'new content' }),
      })
    );
  });

  it('renders the diff container with correct styles', () => {
    const { container } = render(<CodeMirrorDiffViewer originalCode='old' newCode='new' />);
    const outerDiv = container.firstChild;
    expect(outerDiv).toHaveStyle({ width: '100%' });
  });

  it('renders with empty strings', () => {
    const { container } = render(<CodeMirrorDiffViewer originalCode='' newCode='' />);
    expect(container.firstChild).toBeInTheDocument();
  });
});
