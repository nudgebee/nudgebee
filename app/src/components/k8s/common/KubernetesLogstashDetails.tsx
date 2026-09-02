import CodeMirror, { EditorView } from '@uiw/react-codemirror';
import { json } from '@codemirror/lang-json';
import { ds } from '@utils/colors';

// Split out of KubernetesTable so CodeMirror is not pulled into every route that
// renders a table — this is one drilldown branch of the row-detail renderer map.
const KubernetesLogstashDetails = ({ query }: { query: unknown }) => {
  return (
    <CodeMirror
      value={JSON.stringify(query, null, 4)}
      height={ds.space.mul(0, 150)}
      extensions={[json(), EditorView.lineWrapping]}
      editable={false}
      style={{
        border: '1px solid silver',
      }}
    />
  );
};

export default KubernetesLogstashDetails;
