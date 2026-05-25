import CustomBorderCard from '@components1/common/CustomBorderCard';
import FilterDropdown from '@components1/common/FilterDropdownButton';
import { Button as DsButton } from '@components1/ds/Button';
import { Box, Typography } from '@mui/material';
import { useRouter } from 'next/router';
import React, { useEffect, useRef, useState } from 'react';
import ThreeDotLoader from '@components1/common/ThreeDotLoader';
import Text from '@common-new/format/Text';
import CollapsableCard from '@components1/common/widgets/CollapsableCard';
import DeprecatedApis from './cluster-upgrade/cards/DeprecatedApis';
import Pdb from './cluster-upgrade/cards/Pdb';
import HelmUpgrade from './cluster-upgrade/cards/HelmUpgrade';
import apiKubernetes1 from '@api1/kubernetes1';
import { useData } from '@context/DataContext';
import EksAddOn from './cluster-upgrade/cards/EksAddOn';
import KubeVersion from './cluster-upgrade/cards/KubeVersion';

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
        const versions = res?.data?.data?.k8s_versions ?? [];
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
      <CustomBorderCard borderLeftWidth={'8px'} borderLeftColor={'#BFDBFE'} padding={'16px 28px'} sx={{ mb: '20px' }}>
        <Box sx={{ display: 'grid', gridTemplateColumns: '320px 280px 1fr', alignItems: 'center' }}>
          <Box
            sx={{
              '& p': {
                fontSize: '14px',
                fontWeight: 400,
                color: '#9F9F9F',
              },
              '& h2': {
                fontSize: '20px',
                fontWeight: 500,
                color: '#374151',
              },
              '& ul': {
                listStyle: 'none',
                pl: '0px',
                mb: '0px',

                li: {
                  fontSize: '11px',
                  fontWeight: 400,
                  color: '#9F9F9F',
                  lineHeight: '12.89px',
                  paddingBottom: '4px',
                },
              },
            }}
          >
            <Typography>Current Kubernetes Version</Typography>
            <Typography variant='h2'>{currentVersion}</Typography>
          </Box>
          <Box
            sx={{
              '& p': {
                fontSize: '14px',
                fontWeight: 400,
                color: '#9F9F9F',
                paddingBottom: '4px',
              },
              '& ul': {
                listStyle: 'none',
                pl: '0px',
                mb: '0px',
                li: {
                  fontSize: '11px',
                  fontWeight: 400,
                  color: '#9F9F9F',
                  lineHeight: '12.89px',
                  paddingBottom: '4px',
                },
              },
            }}
          >
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
      </CustomBorderCard>
      <Box
        sx={{
          display: 'flex',
          flexDirection: 'column',
          position: 'relative',
          gap: '12px',
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
            <CustomBorderCard borderColor='#93C5FD' sx={{ textAlign: 'center' }} showLeftBorder={false}>
              <Box ref={currentInvestigationRef} sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', gap: '30px' }}>
                <ThreeDotLoader />
                <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', gap: '5px' }}>
                  <Text sx={{ fontWeight: '500', fontSize: '16px', fontStyle: 'italic', color: '#3B82F6' }} value={'Generating'} />
                </Box>
              </Box>
            </CustomBorderCard>
          )}
        </React.Fragment>
      </Box>
    </>
  );
};

export default ClusterUpgradeFeature;
