import { Card } from '@components1/ds/Card';
import FilterDropdown from '@components1/ds/FilterDropdown';
import { Button as DsButton } from '@components1/ds/Button';
import { Box, Typography } from '@mui/material';
import { useRouter } from 'next/router';
import React, { useEffect, useRef, useState } from 'react';
import ThreeDotLoader from '@components1/common/ThreeDotLoader';
import Text from '@common-new/format/Text';
import CollapsableCard from '@common-new/widgets/CollapsableCard';
import DeprecatedApis from './cluster-upgrade/cards/DeprecatedApis';
import Pdb from './cluster-upgrade/cards/Pdb';
import HelmUpgrade from './cluster-upgrade/cards/HelmUpgrade';
import apiKubernetes1 from '@api1/kubernetes1';
import { useData } from '@context/DataContext';
import EksAddOn from './cluster-upgrade/cards/EksAddOn';
import KubeVersion from './cluster-upgrade/cards/KubeVersion';
import { ds } from '@utils/colors';

const availableCards = [DeprecatedApis, Pdb, HelmUpgrade, EksAddOn, KubeVersion];

const ClusterUpgradeFeature = () => {
  const router = useRouter();
  const currentInvestigationRef = useRef();
  const { selectedCluster } = useData();

  const [matchedOptions, setMatchedOptions] = useState([]);
  const [currentInvestigation, setCurrentInvestigation] = useState(null);
  const [isRenderInvestigationCard, setIsRenderInvestigationCard] = useState(false);
  const [collapsedObj, setCollapsedObj] = useState({});
  const [openCardIndex, setOpenCardIndex] = useState(-1);
  const [versionOptions, setVersionOptions] = useState([]);
  const [selectedVersion, setSelectedVersion] = useState('');
  const [isLoading, setIsLoading] = useState(false);
  const [currentVersion, setCurrentVersion] = useState([]);

  const handleCardClick = (index) => {
    setOpenCardIndex(index);
    setCollapsedObj((prevCollapsedObj) => {
      const newCollapsedObj = {};
      if (prevCollapsedObj[index]) {
        newCollapsedObj[index] = false;
      } else {
        newCollapsedObj[index] = true;
      }
      return newCollapsedObj;
    });
  };

  useEffect(() => {
    const currentVersion = selectedCluster?.k8s_version?.match(/^v?(\d+\.\d+)(?=\.\d+)/)?.[1] ?? '';
    setCurrentVersion(currentVersion);
    setVersionOptions([]);
    setSelectedVersion('');
    setMatchedOptions([]);
    setOpenCardIndex(-1);
    setIsLoading(true);
    setCollapsedObj([]);
    setCurrentInvestigation(null);
    setIsRenderInvestigationCard(false);
    apiKubernetes1
      .listK8sVersions()
      .then((res) => {
        const versions = res?.data?.data?.k8s_list_versions ?? [];
        if (versions.length > 0) {
          const k8sVersions = versions
            .sort((a, b) => {
              const dateA = new Date(a.release_date);
              const dateB = new Date(b.release_date);
              return dateA - dateB;
            })
            .map((v) => v.version)
            .filter((g) => g > currentVersion);
          setSelectedVersion(k8sVersions[0]);
          setVersionOptions(k8sVersions);
        }
      })
      .finally(() => {
        setIsLoading(false);
      });
  }, [JSON.stringify(selectedCluster)]);

  useEffect(() => {
    async function renderEvidenceCardAndRecommendations() {
      const filterAvailableCards = availableCards.filter((card) => {
        const cardInstance = new card();
        return cardInstance.id !== 'EksAddOn' || selectedCluster?.k8s_provider === 'EKS';
      });
      for (let C of filterAvailableCards) {
        let card = new C();
        setTimeout(() => {
          setCurrentInvestigation(card);
        }, 1);
        try {
          if (await card.canRenderContent(router?.query?.KubernetesDetails ?? router?.query?.accountId, selectedVersion)) {
            await new Promise((r) => setTimeout(r, 2000));
            setMatchedOptions((old) => [...old, card]);
          }
          setCurrentInvestigation(null);
        } finally {
          setCurrentInvestigation(null);
        }
      }
    }
    if (isRenderInvestigationCard) {
      renderEvidenceCardAndRecommendations();
    }
  }, [isRenderInvestigationCard]);

  const handleVersionChange = (e) => {
    setSelectedVersion(e?.target?.value);
    setIsRenderInvestigationCard(false);
    setMatchedOptions([]);
  };

  return (
    <>
      <Card
        elevation='raised'
        size='md'
        sx={{
          mb: 'var(--ds-space-5)',
          borderLeft: '8px solid var(--ds-blue-300)',
          borderRadius: 'var(--ds-radius-lg)',
          padding: 'var(--ds-space-4) var(--ds-space-5)',
        }}
      >
        <Box sx={{ display: 'grid', gridTemplateColumns: `${ds.space.mul(0, 160)} ${ds.space.mul(0, 140)} 1fr`, alignItems: 'center' }}>
          <Box
            sx={{
              '& p': {
                fontSize: 'var(--ds-text-body)',
                fontWeight: 'var(--ds-font-weight-regular)',
                color: 'var(--ds-gray-500)',
              },
              '& h2': {
                fontSize: 'var(--ds-text-title)',
                fontWeight: 'var(--ds-font-weight-medium)',
                color: 'var(--ds-gray-700)',
              },
              '& ul': {
                listStyle: 'none',
                pl: '0px',
                mb: '0px',
                li: {
                  fontSize: 'var(--ds-text-caption)',
                  fontWeight: 'var(--ds-font-weight-regular)',
                  color: 'var(--ds-gray-500)',
                  lineHeight: 'var(--nb-leading-tight)',
                  paddingBottom: 'var(--ds-space-1)',
                },
              },
            }}
          >
            <Typography>Current Kubernetes Version</Typography>
            <Typography variant='h2'>{currentVersion}</Typography>
          </Box>
          <Box>
            <FilterDropdown
              id='cluster-upgrade-version'
              label='Select a K8s Version'
              options={versionOptions}
              value={selectedVersion}
              onSelect={handleVersionChange}
              disabled={isLoading}
            />
          </Box>
          <Box>
            <DsButton
              id='cluster-upgrade-generate-plan'
              tone='primary'
              size='md'
              disabled={!selectedVersion}
              onClick={() => {
                setIsRenderInvestigationCard(true);
              }}
            >
              Generate Upgrade Plan
            </DsButton>
          </Box>
        </Box>
      </Card>
      <Box
        sx={{
          display: 'flex',
          flexDirection: 'column',
          position: 'relative',
          gap: 'var(--ds-space-3)',
        }}
      >
        <React.Fragment>
          {matchedOptions?.map((option, index) =>
            option ? (
              <CollapsableCard
                key={option?.text}
                idx={index}
                icon={option?.icon}
                text={option?.text}
                resolveButton={option?.resolveButton}
                highlightsData={option?.getHighLightsData()}
                contentComponents={option?.getContentComponents()}
                onCardClick={(idx) => handleCardClick(idx, option)}
                collapsedObj={collapsedObj}
                isCollapsed={collapsedObj[index]}
                expandedCardIndex={openCardIndex}
                resolveButtonClick={option?.resolveButton ? option?.handleResolveButtonClick : null}
                ResolveComponent={option?.resolveButton ? option?.getResolveComponent() : null}
              />
            ) : null
          )}
          {currentInvestigation?.text && (
            <Card
              elevation='flat'
              size='sm'
              sx={{
                textAlign: 'center',
                border: 'none',
                borderBottom: '1px solid var(--ds-blue-300)',
                borderRadius: 'var(--ds-radius-lg)',
              }}
            >
              <Box ref={currentInvestigationRef} sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', gap: 'var(--ds-space-6)' }}>
                <ThreeDotLoader />
                <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
                  <Text
                    sx={{
                      fontWeight: 'var(--ds-font-weight-medium)',
                      fontSize: 'var(--ds-text-body-lg)',
                      fontStyle: 'italic',
                      color: 'var(--ds-blue-500)',
                    }}
                    value={'Generating'}
                  />
                </Box>
              </Box>
            </Card>
          )}
        </React.Fragment>
      </Box>
    </>
  );
};

export default ClusterUpgradeFeature;
