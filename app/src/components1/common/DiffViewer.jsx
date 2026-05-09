import React, { useEffect, useRef } from 'react';
import { basicSetup, EditorView } from 'codemirror';
import { MergeView } from '@codemirror/merge';
import { javascript } from '@codemirror/lang-javascript';
import { withErrorBoundary } from '@common/ErrorBoundary';

const CodeMirrorDiffViewer = ({ originalCode, newCode }) => {
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
    <div style={{ width: '100%', maxWidth: '672px', margin: '16px auto', border: '1px solid #e5e7eb', borderRadius: '8px', overflow: 'hidden' }}>
      <div ref={diffContainerRef} style={{ height: '384px', overflow: 'auto' }} />
    </div>
  );
};

export default withErrorBoundary(CodeMirrorDiffViewer);
