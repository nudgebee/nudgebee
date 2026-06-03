import React, { useState, useEffect, useMemo, useRef, useCallback } from 'react';
import { Box, Typography } from '@mui/material';
import Tooltip from '@components1/ds/Tooltip';
import SafeIcon from '@components1/common/SafeIcon';
import Text from '@common-new/format/Text';
import ThreeDotLoader from '@components1/common/ThreeDotLoader';
import apiKubernetes1 from '@api1/kubernetes1';
import { GetInsightIcon } from '@components1/common/GetInsightIcon';
import { v4 as uuidv4 } from 'uuid';
import HighlightText from './HighlightComponent';
import { getInsightRoute } from './insightRoutes';
import Link from 'next/link';
import { ds } from '@utils/colors';

const MAX_ROWS = 5;

const TruncatedInsight = ({ title, highlightWords, accountId }) => {
  const textRef = useRef(null);
  const [isTruncated, setIsTruncated] = useState(false);

  const checkTruncation = useCallback(() => {
    const el = textRef.current;
    if (el) {
      const child = el.firstElementChild || el;
      setIsTruncated(child.scrollWidth > child.clientWidth);
    }
  }, []);

  useEffect(() => {
    checkTruncation();
    window.addEventListener('resize', checkTruncation);
    return () => window.removeEventListener('resize', checkTruncation);
  }, [checkTruncation, title]);

  return (
    <Tooltip title={isTruncated ? title : ''} arrow placement='top' disableInteractive>
      <Box
        ref={textRef}
        sx={{
          pl: 'var(--ds-space-1)',
          minWidth: 0,
          overflow: 'hidden',
          '& *': { whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' },
        }}
      >
        <HighlightText message={title} highlightWords={highlightWords} cluster={accountId} />
      </Box>
    </Tooltip>
  );
};

const K8sClusterInsights = ({ accountId }) => {
  const [loading, setLoading] = useState(false);
  const [insightData, setInsightData] = useState({});
  const [expanded, setExpanded] = useState(false);
  const [containerWidth, setContainerWidth] = useState(0);
  const containerRef = useRef(null);

  const highlightWords = ['OOMKilled', 'Hi-Restarts', 'right', 'sized'];

  const getInsights = async (accountId) => {
    try {
      const response = await apiKubernetes1.listInsights(accountId);
      const transformedData = Object.keys(response).reduce((acc, key) => {
        acc[key] = response[key].map((item) => {
          const id = uuidv4();
          const appCount = Array.isArray(item.applications) ? item.applications.length : 0;
          const updatedTitle = appCount > 0 ? `${appCount} ${item.title}` : item.title;

          return {
            ...item,
            id,
            title: updatedTitle,
            icon: GetInsightIcon({ ...item, id }),
          };
        });
        return acc;
      }, {});
      setInsightData(transformedData);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (accountId) {
      setLoading(true);
      getInsights(accountId);
    }
  }, [accountId]);

  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const observer = new ResizeObserver((entries) => {
      for (const entry of entries) {
        setContainerWidth(entry.contentRect.width);
      }
    });
    observer.observe(el);
    return () => observer.disconnect();
  }, []);

  const insights = insightData[accountId] || [];

  // Dynamic columns based on data count AND available width
  const columnCount = useMemo(() => {
    const count = insights.length;
    let cols;
    if (count <= 3) cols = 1;
    else if (count <= 8) cols = 2;
    else cols = 3;
    // Cap columns based on container width
    if (containerWidth > 0) {
      if (containerWidth < 400) cols = Math.min(cols, 1);
      else if (containerWidth < 650) cols = Math.min(cols, 2);
    }
    return cols;
  }, [insights.length, containerWidth]);

  const visibleCount = MAX_ROWS * columnCount;
  const hasMore = insights.length > visibleCount;
  const visibleInsights = expanded ? insights : insights.slice(0, visibleCount);

  const columns = useMemo(() => {
    const rowsPerCol = Math.ceil(visibleInsights.length / columnCount);
    const cols = [];
    for (let i = 0; i < columnCount; i++) {
      cols.push(visibleInsights.slice(i * rowsPerCol, (i + 1) * rowsPerCol));
    }
    return cols;
  }, [visibleInsights, columnCount]);

  return (
    <Box
      ref={containerRef}
      sx={{
        minHeight: ds.space[7],
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--ds-space-1)',
        borderRadius: 'var(--ds-radius-lg)',
        p: 'var(--ds-space-3) var(--ds-space-3)',
        background: 'var(--ds-background-200)',
        border: '1px solid var(--ds-gray-200)',
        flex: 1,
        overflow: 'hidden',
      }}
    >
      <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <Text
          value={'Insights'}
          sx={{ fontWeight: 'var(--ds-font-weight-semibold)', fontSize: 'var(--ds-text-body)', color: 'var(--ds-brand-500)' }}
        />
        {hasMore && (
          <Typography
            onClick={() => setExpanded((prev) => !prev)}
            sx={{
              fontSize: 'var(--ds-text-small)',
              color: 'var(--ds-blue-500)',
              cursor: 'pointer',
              fontWeight: 'var(--ds-font-weight-medium)',
              userSelect: 'none',
              '&:hover': { textDecoration: 'underline' },
            }}
          >
            {expanded ? 'Show less' : `+${insights.length - visibleCount} more`}
          </Typography>
        )}
      </Box>
      {loading ? (
        <Box sx={{ display: 'flex', justifyContent: 'center', py: 'var(--ds-space-2)' }}>
          <ThreeDotLoader />
        </Box>
      ) : insights.length > 0 ? (
        <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)' }}>
          {columns.map((col, colIndex) => (
            <Box
              key={colIndex}
              sx={{
                flex: `1 1 ${100 / columnCount}%`,
                minWidth: 0,
                display: 'flex',
                flexDirection: 'column',
                gap: 'var(--ds-space-1)',
              }}
            >
              {col.map((d) => {
                const route = getInsightRoute(d.title, accountId, 'K8s', d.rule);
                const content = (
                  <>
                    {d.icon ? (
                      <Box sx={{ flexShrink: 0 }}>
                        <SafeIcon src={d.icon} alt='start-icon' priority={true} />
                      </Box>
                    ) : null}
                    <TruncatedInsight title={d.title} highlightWords={highlightWords} accountId={accountId} />
                  </>
                );
                const rowSx = {
                  display: 'flex',
                  alignItems: 'center',
                  py: 'var(--ds-space-1)',
                  px: 'var(--ds-space-1)',
                  borderRadius: 'var(--ds-radius-md)',
                  transition: 'background-color 0.15s ease',
                  minWidth: 0,
                  textDecoration: 'none',
                  color: 'inherit',
                };
                return route ? (
                  <Link key={d.id} href={route} style={{ textDecoration: 'none', color: 'inherit' }}>
                    <Box
                      sx={{
                        ...rowSx,
                        cursor: 'pointer',
                        '&:hover': { backgroundColor: 'var(--ds-background-300)' },
                      }}
                    >
                      {content}
                    </Box>
                  </Link>
                ) : (
                  <Box key={d.id} sx={rowSx}>
                    {content}
                  </Box>
                );
              })}
            </Box>
          ))}
        </Box>
      ) : (
        <Typography sx={{ color: 'var(--ds-gray-400)', fontSize: 'var(--ds-text-body)', py: 'var(--ds-space-1)' }}>No Insights Available</Typography>
      )}
    </Box>
  );
};

export default K8sClusterInsights;
