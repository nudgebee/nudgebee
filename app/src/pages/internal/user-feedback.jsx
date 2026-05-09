import React, { useEffect, useState } from 'react';
import BoxLayout2 from '@components1/common/BoxLayout2';
import apiAskNudgebee from '@api1/ask-nudgebee';
import apiUsers from '@api1/user';
import CustomTable from '@components1/common/tables/CustomTable2';
import Link from 'next/link';
import { useRouter } from 'next/router';
import { DEFAULT_TITLE } from '@hooks/useTenantBranding';

const HEADERS_USER_FEEDBACK = ['Module', 'Useful', 'Question', 'Answer', 'AdditionalDetails', 'User'];

const UserFeedback = () => {
  const router = useRouter();
  const [kubeId, setKubeId] = useState(router.query.accountId);

  const [data, setData] = useState([]);
  const [selectedModule, setSelectedModule] = useState('new-investigation');
  const [selectedUseful, setSelectedUseful] = useState('');
  const moduleNames = {
    investigate: 'Troubleshoot',
    loki: 'Loki Query',
    pometheus: 'Prometheus Query',
    es: 'ElasticSearch Query',
    'new-investigation': `Ask ${DEFAULT_TITLE}`,
  };
  const modules = ['investigate', 'loki', 'prometheus', 'es', 'new-investigation'].map((item) => ({
    label: moduleNames[item] ?? item,
    value: item,
  }));
  const [, setRowsPerPage] = useState(100);
  const usefulness = ['true', 'false'];
  const [loading, setLoading] = useState(false);
  const [currentPage, setCurrentPage] = useState(0);

  const onPageChange = (page, limit) => {
    setCurrentPage(page - 1);
    setRowsPerPage(limit);
  };

  useEffect(() => {
    if (kubeId !== router.query.accountId) {
      setKubeId(router.query.accountId);
    }
  }, [router.query.accountId]);

  useEffect(() => {
    let query = {};
    if (selectedModule) {
      query.module = selectedModule;
    }
    if (selectedUseful) {
      query.useful = selectedUseful === 'true';
    }
    if (kubeId) {
      query.cloud_account_id = kubeId;
    }
    setLoading(true);

    apiUsers.listUsers({ status: 'active' }).then((res) => {
      let userIdNameMap = {};
      res.data?.forEach((item) => {
        userIdNameMap[item.id] = item.username;
      });
      apiAskNudgebee.listAiFeedback(query).then((res) => {
        let tableData = res.data?.data?.llm_conversation_feedback?.rows?.map((item) => {
          let moduleHref = '';
          if (item.module === 'investigate') {
            moduleHref = `/investigate?id=${item.conversation_id}&accountId=${item.cloud_account_id}`;
          } else if (item.module === 'new-investigation') {
            moduleHref = `/ask-nudgebee?accountId=${item.cloud_account_id}&session_id=${item.conversation_id}&message_id=${item.session_id}`;
          }

          return [
            {
              text: moduleHref ? (
                <Link href={moduleHref} target='_blank'>
                  {moduleNames[item.module] ?? item.module}
                </Link>
              ) : (
                item.module
              ),
            },
            {
              text: item.useful === true ? 'Yes' : 'No',
            },
            {
              text: item.question,
            },
            {
              text: item.llm_response,
            },
            {
              text: item.additional_notes,
            },
            {
              text: userIdNameMap[item.user_id] ?? item.user_id,
            },
          ];
        });
        setData(tableData || []);
        setLoading(false);
      });
    });
  }, [selectedModule, selectedUseful, currentPage, kubeId, moduleNames]);

  return (
    <BoxLayout2
      id='user-feedback'
      heading=''
      sharingOptions={{
        download: {
          enabled: false,
        },
        sharing: { enabled: false },
      }}
      filterOptions={[
        {
          type: 'dropdown',
          enabled: true,
          options: modules,
          onSelect: (e) => {
            setSelectedModule(e?.target?.value);
            setCurrentPage(0);
          },
          minWidth: '150px',
          label: 'Module',
          value: selectedModule,
        },
        {
          type: 'dropdown',
          enabled: true,
          options: usefulness,
          onSelect: (e) => {
            setSelectedUseful(e?.target?.value);
            setCurrentPage(0);
          },
          minWidth: '150px',
          label: 'Useful',
          value: selectedUseful,
        },
      ]}
    >
      <CustomTable
        rowsPerPage={100}
        onPageChange={onPageChange}
        pageNumber={currentPage + 1}
        headers={HEADERS_USER_FEEDBACK}
        tableData={data}
        loading={loading}
      />
    </BoxLayout2>
  );
};

export default UserFeedback;
