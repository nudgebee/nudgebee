import apiKubernetes from '@api1/kubernetes';
import { useEffect, useState } from 'react';
import { useRouter } from 'next/router';
import BoxLayout2 from '@components1/common/BoxLayout2';
import KubernetesTable2 from './KubernetesTable2';
import Datetime from '@components1/common/format/Datetime';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import apiUser from '@api1/user';
import { Text } from '@components1/common';

interface KubernetesDeploymentHistoryProps {
  accountId: string;
  cloudResourceId: string;
  subjectName: string;
  subjectNamespace: string;
  heading: string;
  subjectType: string;
}

interface DrillDownQueryProps {
  id: string;
}

const deploymentTableHeader = ['Name', 'Time', 'Status', 'Description', ''];

const KubernetesDeploymentHistory: React.FC<KubernetesDeploymentHistoryProps> = ({
  accountId = '',
  cloudResourceId = '',
  subjectName = '',
  subjectNamespace = '',
  subjectType = '',
  heading = '',
}) => {
  const [currentPage, setCurrentPage] = useState<number>(0);
  const [rowsPerPage, setRowsPerPage] = useState<number>(apiUser.getUserPreferencesTablePageSize());
  const [deploymentsData, setDeploymentsData] = useState([]);
  const [totalDeployments, setTotalDeployments] = useState(0);
  const [loading, setLoading] = useState<boolean>(false);
  const router = useRouter();

  if (!accountId) {
    accountId = (router?.query?.accountId as string) || (router.query?.KubernetesDetails as string) || '';
  }

  const getKubernetesDeployments = async () => {
    try {
      const limit = rowsPerPage;
      const subject_type = subjectType?.toLocaleLowerCase() || 'deployment';
      const aggregation_key = 'ConfigurationChange/KubernetesResource/Change';
      const resource_id = cloudResourceId;
      const account_id = accountId;
      const subject_name = subjectName;
      setLoading(true);
      await apiKubernetes
        .getK8sEvents(
          limit,
          currentPage,
          {
            subject_name,
            account_id,
            subject_type,
            aggregation_key,
            resource_id,
            subject_namespace: subjectNamespace,
            endDate: new Date(),
            startDate: new Date(new Date().setDate(new Date().getDate() - 90)),
          },
          ['title', 'subject_name', 'starts_at', 'description', 'status']
        )
        .then((response: any) => {
          const allDeploymentsData = response?.data?.events.map((e: any) => {
            const data: any = [];
            data.push({ text: <Text value={e?.title} showAutoEllipsis sx={{ minWidth: '200px' }} /> });
            data.push({ component: <Datetime baseDate={new Date()} value={e?.starts_at} />, drilldownQuery: { id: e.id } as DrillDownQueryProps });
            data.push({ component: <CustomLabels margin='auto' text={e?.status} /> });
            data.push({ text: <Text value={e?.description} showAutoEllipsis /> });
            data.push({ text: '' });
            return data;
          });
          setDeploymentsData(allDeploymentsData);
          setTotalDeployments(response?.data?.events_aggregate?.aggregate?.count);
          setLoading(false);
        });
    } catch (error) {
      console.error(error);
      setLoading(false);
    }
  };

  useEffect(() => {
    getKubernetesDeployments();
  }, [currentPage, rowsPerPage, accountId, cloudResourceId, subjectName, subjectNamespace, subjectType]);

  return (
    <BoxLayout2 id={'id'} heading={heading}>
      <KubernetesTable2
        headers={deploymentTableHeader}
        data={deploymentsData}
        expandable={{
          tabs: [
            {
              text: 'Diff',
              value: 0,
              key: 'deployment-diff',
            },
          ],
        }}
        upperHeaders={[]}
        showExpandable={true}
        rowsPerPage={rowsPerPage}
        totalRows={totalDeployments}
        onPageChange={(e: number, l: number) => {
          setCurrentPage(e);
          setRowsPerPage(l);
        }}
        sort={{}}
        onSortChange={{}}
        loading={loading}
        tableHeadingCenter={['Status']}
        pageNumber={currentPage}
      />
    </BoxLayout2>
  );
};

export default KubernetesDeploymentHistory;
