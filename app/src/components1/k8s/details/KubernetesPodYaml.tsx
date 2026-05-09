import React, { useEffect, useState } from 'react';
import k8sApi from '@api1/kubernetes';
import { Typography } from '@mui/material';
import CodeMirror, { EditorView } from '@uiw/react-codemirror';
import { yaml as yaml1 } from '@codemirror/lang-yaml';
import yaml from 'js-yaml';
import Loader from '@components1/common/Loader';
import CustomButton from '@components1/common/NewCustomButton';
import { snackbar } from '@components1/common/snackbarService';
import { hasWriteAccess } from '@lib/auth';

interface KubernetesPodYamlProps {
  accountId: string;
  query: Record<string, any>;
  showEditButton: boolean;
}

const KubernetesPodYaml: React.FC<KubernetesPodYamlProps> = ({ accountId, query, showEditButton = false }) => {
  const [text, setText] = useState('');
  const [fileName, setFileName] = useState('');
  const [errorMsg, setErrorMsg] = useState('');
  const [allowEdit, setAllowEdit] = useState(false);

  useEffect(() => {
    const data = {
      no_sinks: true,
      body: {
        account_id: accountId,
        action_name: 'get_resource_yaml',
        action_params: {
          name: query.workload_name || query.subject_name || query.pod_name,
          namespace: query.namespace_name || query.subject_namespace,
          kind: query?.kind || query?.subject_kind,
        },
        origin: 'Nudgebee UI',
      },
    };
    k8sApi
      .relayForwardRequest(data)
      .then((res) => {
        if (res?.data?.success) {
          const findings = res?.data.findings;
          if (findings && findings.length > 0) {
            for (const element of findings) {
              if (element?.evidence.length > 0) {
                for (const evi of element.evidence) {
                  if (evi?.data) {
                    const parsedData = JSON.parse(evi?.data);
                    for (const d of parsedData) {
                      if (d.type === 'yaml') {
                        setFileName(d.filename);
                        const text = atob(d.data.slice(2, -1));
                        setText(text);
                        break;
                      }
                    }
                  }
                }
              }
            }
          }
        } else {
          setErrorMsg('No Yaml Found');
        }
      })
      .catch(() => {
        setErrorMsg('Failed to fetch the Yaml');
      });
  }, []);

  const handleSubmitOfEdit = () => {
    if (!hasWriteAccess()) {
      snackbar.error('No Access to Edit the Workload');
      return;
    }
    let jsonObj;
    try {
      jsonObj = yaml.load(text);
    } catch {
      snackbar.error('Invalid YAML');
      return;
    }
    const data = {
      no_sinks: true,
      body: {
        account_id: accountId,
        action_name: 'replace_workload',
        action_params: {
          name: query.workload_name,
          namespace: query.namespace_name,
          kind: query?.kind,
          [query?.kind.toLowerCase()]: jsonObj,
        },
        origin: 'Nudgebee UI',
      },
    };
    k8sApi
      .relayForwardRequest(data)
      .then((res) => {
        if (res?.data?.success) {
          setAllowEdit(false);
          snackbar.success(`${query?.kind} ${query?.workload_name} is updated`);
        } else {
          const message = res.data.msg;
          const httpBodyError = message.split('HTTP response body: ')[1];
          if (httpBodyError) {
            const httpBody = JSON.parse(httpBodyError);
            const msg = httpBody.details.causes.map((c: any) => c.field + ':' + c.reason).join(';');
            snackbar.error(msg);
            return;
          }
          snackbar.error(`${query?.kind} ${query?.workload_name} is failed`);
        }
      })
      .catch(() => {
        snackbar.error(`${query?.kind} ${query?.workload_name} is failed`);
      });
  };

  const renderContent = () => {
    if (!errorMsg && !text) {
      return <Loader style={{ width: '100%' }} />;
    } else if (errorMsg) {
      return <Typography>{errorMsg}</Typography>;
    }
    return (
      <>
        {showEditButton && hasWriteAccess() && (
          <div style={{ marginBottom: '10px' }}>
            <CustomButton variant='tertiary' text='Edit Yaml' onClick={() => setAllowEdit(true)} size='Medium' id='allow-edit' type='button' />
          </div>
        )}

        <CodeMirror
          value={text}
          height='500px'
          extensions={[yaml1(), EditorView.lineWrapping]}
          editable={allowEdit}
          style={{
            border: '1px solid silver',
            backgroundColor: allowEdit ? 'white' : '#f5f5f5',
            opacity: allowEdit ? 1 : 0.6,
            cursor: allowEdit ? 'text' : 'not-allowed',
            marginBottom: '15px',
          }}
          onChange={(value) => {
            setText(value);
          }}
        />

        {allowEdit && showEditButton && (
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
            <div style={{ display: 'flex', gap: '10px' }}>
              <CustomButton variant='tertiary' text='Cancel' onClick={() => setAllowEdit(false)} size='Medium' id='cancel' type='button' />
              <CustomButton variant='primary' text='Submit' onClick={() => handleSubmitOfEdit()} size='Medium' id='submit' type='button' />
            </div>
          </div>
        )}
      </>
    );
  };

  return (
    <div style={{ paddingLeft: '15px' }}>
      <Typography sx={{ paddingTop: '10px', paddingBottom: '20px' }}>{fileName}</Typography>
      {renderContent()}
    </div>
  );
};

export default KubernetesPodYaml;
