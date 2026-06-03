import { useState, useEffect } from 'react';
import recommendationApi, { RECOMMENDATION_SERVERITY, RECOMMENDATION_STATUS } from '@api1/recommendation';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import CustomSearch from '@common-new/CustomSearch';
import { ToggleGroup } from '@components1/ds/ToggleGroup';
import DownloadButton from '@common-new/DownloadButton';
import PropTypes from 'prop-types';
import KubernetesSecurityDetails from './security/KubernetesSecurityDetails';
import KubernetesSecurityApps from './security/KubernetesSecurityApps';
import KubernetesSecurityImages from './security/KubernetesSecurityImages';
import KubernetesSecurityCVE from './security/KubernetesSecurityCVE';
import { useRouter } from 'next/router';
import { applyFiltersOnRouter } from '@lib/router';
import { syncFilterFromQuery } from '@utils/common';

const KubernetesSecurity = (props) => {
  const router = useRouter();

  const [recommendationStatus, setRecommendationStatus] = useState('Open');
  const [recommendationSeverity, setRecommendationSeverity] = useState([]);
  const [recommendationImage, setRecommendationImage] = useState(props.filters?.image ?? '');
  const [imageInput, setImageInput] = useState(props.filters?.image ?? '');
  const [namespaces, setNamespaces] = useState([]);
  const [selectedNamespace, setSelectedNamespace] = useState(router.query.namespace);
  const [workloads, setWorkloads] = useState([]);
  const [selectedWorkload, setSelectedWorkload] = useState(props?.workload_name ?? '');
  const [activeToggleButton, setActiveToggleButton] = useState(props.activeToggleButton ?? 'apps');
  const [resetPage, setResetPage] = useState('');

  useEffect(() => {
    if (!router?.query?.severity) return;
    setRecommendationSeverity(syncFilterFromQuery(RECOMMENDATION_SERVERITY, router?.query?.severity));
  }, [router?.query?.severity]);

  useEffect(() => {
    if (!props?.kubernetes?.id) {
      return;
    }
    recommendationApi
      .listRecommendationNamesapces({
        accountId: props?.kubernetes?.id,
        category: 'Security',
        status: recommendationStatus,
      })
      .then((res) => {
        setNamespaces(res);
      });
  }, [props?.kubernetes?.id, recommendationStatus]);

  useEffect(() => {
    if (!props?.kubernetes?.id) {
      return;
    }
    if (!selectedNamespace) {
      return;
    }
    recommendationApi
      .listRecommendationWorkloads({
        accountId: props?.kubernetes?.id,
        category: 'Security',
        status: recommendationStatus,
        namespaceName: selectedNamespace,
      })
      .then((res) => {
        setWorkloads(res);
      });
  }, [props?.kubernetes?.id, recommendationStatus, selectedNamespace]);

  const filterOptions = [
    {
      type: 'dropdown',
      label: 'Status',
      width: '140px',
      // InProgress is intentionally excluded — security findings are acted on via ticket, not tracked as in-progress here
      options: RECOMMENDATION_STATUS.filter((s) => s !== 'InProgress'),
      value: recommendationStatus,
      enabled: props?.enableFilters?.includes('status') ?? true,
      onSelect: function (e, _rule) {
        setRecommendationStatus(e?.target?.value);
        setResetPage(`status-${e?.target?.value}`);
      },
    },
    {
      type: 'multi-dropdown',
      label: 'Severity',
      minWidth: '140px',
      options: RECOMMENDATION_SERVERITY,
      value: recommendationSeverity,
      enabled: props?.enableFilters?.includes('severity') ?? true,
      onSelect: function (e) {
        setRecommendationSeverity(e?.target?.value ?? []);
        setResetPage(`severity-${e?.target?.value?.join(',')}`);
      },
    },
    {
      type: 'dropdown',
      label: 'Namespace',
      width: '155px',
      options: namespaces,
      value: selectedNamespace,
      enabled: props?.enableFilters?.includes('namespace') ?? true,
      onSelect: function (e, _rule) {
        setSelectedNamespace(e?.target?.value);
        setSelectedWorkload('');
        setWorkloads([]);
        applyFiltersOnRouter(router, { namespace: e?.target?.value });
        setResetPage(`namespace-${e?.target?.value}`);
      },
    },
    {
      type: 'dropdown',
      label: 'Workload',
      width: '155px',
      options: workloads,
      value: selectedWorkload,
      enabled: props?.enableFilters?.includes('workload') ?? true,
      onSelect: function (e, _rule) {
        setSelectedWorkload(e?.target?.value);
        setResetPage(`workload-${e?.target?.value}`);
      },
    },
  ];

  const showImageSearch = activeToggleButton === 'images' || activeToggleButton === 'details';

  return (
    <ListingLayout id='security-best-practices'>
      <ListingLayout.Toolbar
        actions={
          <>
            <ToggleGroup
              selection='single'
              value={activeToggleButton}
              onChange={(next) => setActiveToggleButton(next)}
              ariaLabel='Security view'
              options={[
                { value: 'apps', label: 'Apps' },
                { value: 'images', label: 'Images' },
                { value: 'cve', label: 'CVE' },
                { value: 'details', label: 'Details' },
              ]}
            />
            <DownloadButton onClick={() => ({ tableId: activeToggleButton })} />
          </>
        }
      >
        {filterOptions
          .filter((f) => f.enabled !== false && f.type !== 'search')
          .map((f) => (
            <FilterDropdown
              key={f.label}
              id={`security-filter-${f.label?.toLowerCase()}`}
              label={f.label}
              options={f.options}
              value={f.value}
              multiple={f.type === 'multi-dropdown'}
              onSelect={f.onSelect}
            />
          ))}
        {showImageSearch && (props?.enableFilters?.includes('image') ?? true) && (
          <CustomSearch
            id='security-filter-image'
            value={imageInput}
            onChange={(next) => {
              setImageInput((prev) => {
                if (prev.trim() !== '' && next.trim() === '') {
                  setRecommendationImage('');
                }
                return next;
              });
            }}
            onEnterPress={() => setRecommendationImage(imageInput)}
            onClear={() => {
              setImageInput('');
              setRecommendationImage('');
            }}
            label='Image'
          />
        )}
      </ListingLayout.Toolbar>
      <ListingLayout.Body>
        {activeToggleButton == 'apps' && (
          <KubernetesSecurityApps
            kubernetes={{ id: props?.kubernetes?.id }}
            query={{
              workload_name: selectedWorkload,
              namespace: selectedNamespace,
              severity: recommendationSeverity.length > 0 ? recommendationSeverity : undefined,
              status: recommendationStatus,
            }}
            tableId={activeToggleButton}
            disableInfographic={props?.disableInfographic}
            resetPage={resetPage}
          />
        )}
        {activeToggleButton == 'details' && (
          <KubernetesSecurityDetails
            kubernetes={{ id: props?.kubernetes?.id }}
            query={{
              workload_name: selectedWorkload,
              namespace: selectedNamespace,
              severity: recommendationSeverity.length > 0 ? recommendationSeverity : undefined,
              status: recommendationStatus,
              image: recommendationImage,
            }}
            tableId={activeToggleButton}
            disableInfographic={props?.disableInfographic}
          />
        )}
        {activeToggleButton == 'images' && (
          <KubernetesSecurityImages
            kubernetes={{ id: props?.kubernetes?.id }}
            query={{
              workload_name: selectedWorkload,
              namespace: selectedNamespace,
              severity: recommendationSeverity.length > 0 ? recommendationSeverity : undefined,
              status: recommendationStatus,
              image: recommendationImage,
            }}
            tableId={activeToggleButton}
            disableInfographic={props?.disableInfographic}
            resetPage={resetPage}
          />
        )}
        {activeToggleButton == 'cve' && (
          <KubernetesSecurityCVE
            kubernetes={{ id: props?.kubernetes?.id }}
            query={{
              workload_name: selectedWorkload,
              namespace: selectedNamespace,
              severity: recommendationSeverity.length > 0 ? recommendationSeverity : undefined,
              status: recommendationStatus,
            }}
            tableId={activeToggleButton}
            disableInfographic={props?.disableInfographic}
            resetPage={resetPage}
          />
        )}
      </ListingLayout.Body>
    </ListingLayout>
  );
};

KubernetesSecurity.propTypes = {
  heading: PropTypes.string,
  kubernetes: PropTypes.object,
  enableFilters: PropTypes.array,
  filters: PropTypes.object,
  disableInfographic: PropTypes.bool,
  workload_name: PropTypes.string,
  activeToggleButton: PropTypes.string,
};

export default KubernetesSecurity;
