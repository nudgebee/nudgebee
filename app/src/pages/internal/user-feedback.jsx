import React, { useEffect, useState } from 'react';
import ListingLayout from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import { Label } from '@components1/ds/Label';
import apiAskNudgebee from '@api1/ask-nudgebee';
import apiUsers from '@api1/user';
import CustomTable from '@common-new/tables/CustomTable2';
import { Link } from '@components1/ds/Link';
import { useRouter } from 'next/router';
import { DEFAULT_TITLE } from '@hooks/useTenantBranding';

const HEADERS_USER_FEEDBACK = ['Module', 'Useful', 'Question', 'Answer', 'AdditionalDetails', 'User'];

const MODULE_NAMES = {
  investigate: 'Troubleshoot',
  loki: 'Loki Query',
  pometheus: 'Prometheus Query',
  es: 'ElasticSearch Query',
  'new-investigation': `Ask ${DEFAULT_TITLE}`,
};

const MODULES = ['investigate', 'loki', 'prometheus', 'es', 'new-investigation'].map((item) => ({
  label: MODULE_NAMES[item] ?? item,
  value: item,
}));

const UserFeedback = () => {
  const router = useRouter();
  const [kubeId, setKubeId] = useState(router.query.accountId);

  const [data, setData] = useState([]);
  const [selectedModule, setSelectedModule] = useState('new-investigation');
  const [selectedUseful, setSelectedUseful] = useState('');
  const usefulness = [
    { label: 'Yes', value: 'true' },
    { label: 'No', value: 'false' },
  ];
  const [loading, setLoading] = useState(false);

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
                <Link href={moduleHref} openInNew>
                  {MODULE_NAMES[item.module] ?? item.module}
                </Link>
              ) : (
                item.module
              ),
            },
            {
              component: <Label text={item.useful === true ? 'Yes' : 'No'} variant={item.useful === true ? 'green' : 'red'} />,
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
  }, [selectedModule, selectedUseful, kubeId]);

  return (
    <ListingLayout id='user-feedback'>
      <ListingLayout.Toolbar>
        <FilterDropdown label='Module' options={MODULES} value={selectedModule} onSelect={(e) => setSelectedModule(e?.target?.value)} size='sm' />
        <FilterDropdown label='Useful' options={usefulness} value={selectedUseful} onSelect={(e) => setSelectedUseful(e?.target?.value)} size='sm' />
      </ListingLayout.Toolbar>
      <ListingLayout.Body>
        <CustomTable headers={HEADERS_USER_FEEDBACK} tableData={data} loading={loading} />
      </ListingLayout.Body>
    </ListingLayout>
  );
};

export default UserFeedback;
