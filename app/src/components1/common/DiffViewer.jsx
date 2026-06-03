import React, { useEffect, useRef } from 'react';
import { basicSetup, EditorView } from 'codemirror';
import { MergeView } from '@codemirror/merge';
import { javascript } from '@codemirror/lang-javascript';
import { withErrorBoundary } from '@common/ErrorBoundary';

const CodeMirrorDiffViewer = ({ originalCode, newCode, leftLabel, rightLabel, maxWidth = '100%' }) => {
  const diffContainerRef = useRef(null);

  useEffect(() => {
    if (!diffContainerRef.current) {
      return;
    }

    const mergeView = new MergeView({
      a: {
        doc: originalCode,
        extensions: [basicSetup, javascript(), EditorView.lineWrapping],
      },
      b: {
        doc: newCode,
        extensions: [basicSetup, javascript(), EditorView.lineWrapping],
      },
      parent: diffContainerRef.current,
      gutter: true,
      collapseUnchanged: false,
    });
    return () => {
      mergeView.destroy();
    };
  }, [originalCode, newCode]);

  return (
    <div className='cm-diff-viewer-bright' style={{ width: '100%', maxWidth, margin: 'var(--ds-space-4) auto' }}>
      <style>{`
        .cm-diff-viewer-bright .cm-deletedChunk { background-color: #ffd4d4 !important; }
        .cm-diff-viewer-bright .cm-changedLine { background-color: #fff3a8 !important; }
        .cm-diff-viewer-bright .cm-deletedText,
        .cm-diff-viewer-bright .cm-deletedChunk .cm-deletedText {
          background-color: #ff9b9b !important;
          color: #5a0000 !important;
          text-decoration: none !important;
        }
        .cm-diff-viewer-bright .cm-changedText {
          background-color: #ffe066 !important;
          color: #4a3500 !important;
        }
        .cm-diff-viewer-bright .cm-insertedLine,
        .cm-diff-viewer-bright .cm-insertedLine .cm-changedText {
          background-color: #a8e6a8 !important;
          color: #0a3d0a !important;
          text-decoration: none !important;
        }
        .cm-diff-viewer-bright .cm-changeGutter { background-color: transparent !important; }
        .cm-diff-viewer-bright .cm-changedLineGutter { background-color: transparent !important; }
        .cm-diff-viewer-bright .cm-deletedLineGutter { background-color: transparent !important; }
      `}</style>
      {(leftLabel || rightLabel) && (
        <div style={{ display: 'flex', marginBottom: 'var(--ds-space-2)' }}>
          <div style={{ flex: 1, display: 'flex', alignItems: 'center' }}>{leftLabel}</div>
          <div style={{ flex: 1, display: 'flex', alignItems: 'center' }}>{rightLabel}</div>
        </div>
      )}
      <div style={{ border: '1px solid var(--ds-brand-150)', borderRadius: 'var(--ds-radius-lg)', overflow: 'hidden' }}>
        <div ref={diffContainerRef} style={{ maxHeight: '400px', overflow: 'auto' }} />
      </div>
    </div>
  );
};

export default withErrorBoundary(CodeMirrorDiffViewer);
