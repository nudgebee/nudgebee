import { useState, useRef, useEffect } from 'react';
import {
  Box,
  TextField,
  Paper,
  Typography,
  List,
  ListItem,
  ListItemText,
  Chip,
  InputAdornment,
  IconButton,
  Tooltip,
  Popper,
  CircularProgress,
  ListItemButton,
} from '@mui/material';
import {
  Label as LabelIcon,
  FilterList as FilterIcon,
  Search as SearchIcon,
  Close as CloseIcon,
  InfoOutlined as InfoIcon,
  Storage as ValueIcon,
  WarningAmber as WarningIcon,
  Add as AddIcon,
} from '@mui/icons-material';
import { snackbar } from '@components1/common/snackbarService';
import { parseHttpResponseBodyMessage } from 'src/utils/common';
import observability from '@api1/observability';
import CustomButton from '@components1/common/NewCustomButton';
import DeleteButton from '@components1/k8s/common/DeleteButton';
import FilterDropdownButton from '@components1/common/FilterDropdownButton';
import { generateQuery } from './LogGenerateQuery';
import { getLineOperators } from './QueryBuilder';
import { getOperatorsForKind, getOperatorDisplayLabel, normalizeLegacyOperator } from './operatorCatalog';

// TODO: move server-side when service_map/knowledge_graph gain a backend
// GetSupportedOperators. Both are served by triage/service_map.go and
// traces/knowledge_graph_service.go which don't implement the LogSource /
// TraceSource operator interface today.
const SERVICE_MAP_OPERATORS = [
  { value: '_eq', label: '= (equals)', description: 'Exact match' },
  { value: '_neq', label: '!= (not equals)', description: 'Not equal to' },
  { value: '_lt', label: '< (less than)', description: 'Less than a given value' },
  { value: '_lte', label: '<= (less than or equal)', description: 'Less than or equal to a given value' },
  { value: '_gt', label: '> (greater than)', description: 'Greater than a given value' },
  { value: '_gte', label: '>= (greater than or equal)', description: 'Greater than or equal to a given value' },
  { value: '_in', label: 'IN (in list)', description: 'Matches any value in the list' },
  { value: '_not_in', label: 'NOT IN (not in list)', description: 'Does not match any value in the list' },
  { value: '_like', label: 'LIKE (pattern match)', description: 'Matches using SQL LIKE pattern (e.g. %value%)' },
  { value: '_nlike', label: 'NOT LIKE (not pattern match)', description: 'Does not match SQL LIKE pattern' },
  { value: '_contains', label: 'CONTAINS', description: 'Checks if value contains a substring' },
  { value: '_ilike', label: 'ILIKE (case-insensitive like)', description: 'Case-insensitive pattern match' },
  { value: '_has_key', label: 'HAS KEY', description: 'Checks if JSON/Map has the given key' },
  { value: '_is_null', label: 'IS NULL', description: 'Checks if the value is null' },
  { value: '_between', label: 'BETWEEN', description: 'Checks if value lies within a range' },
];

const KNOWLEDGE_GRAPH_OPERATORS = [{ value: '=', label: '= (equals)', description: 'Exact match' }];
import { colors } from 'src/utils/colors';
import cache from '@lib/cache';
import { v4 as uuidv4 } from 'uuid';
import apiKubernetes1 from '@api1/kubernetes1';
import CustomTooltip from '@components1/common/CustomTooltip';

// Normalize legacy UI operator values (CONTAINS, NOT ILIKE, =, ...) on chips
// and line operations so hydrated state from URL params, persisted blocks, or
// saved queries matches the backend-token vocabulary the catalog now emits.
// service_map/knowledge_graph are opted out — they consume chip.operator verbatim
// and keep their own legacy vocabularies (see LogGenerateQuery.js service_map branch).
const shouldNormalizeOperators = (provider) => provider !== 'service_map' && provider !== 'knowledge_graph';
const normalizeChip = (chip) => (chip?.operator ? { ...chip, operator: normalizeLegacyOperator(chip.operator) } : chip);
const normalizeOperation = (op) => (op?.op ? { ...op, op: normalizeLegacyOperator(op.op) } : op);
const normalizeBlock = (block) => ({
  ...block,
  queryItems: Array.isArray(block?.queryItems) ? block.queryItems.map(normalizeChip) : block?.queryItems,
  operations: Array.isArray(block?.operations) ? block.operations.map(normalizeOperation) : block?.operations,
});

const DEFAULT_QUERY_BLOCK = () => ({
  id: 0,
  query_key: uuidv4(),
  selectedMetric: '',
  queryItems: [],
  operations: [],
  aggregator: 'avg',
  aggregatorBy: [],
});

const initializeQueryBlocks = (prebuildQueryBlocks, logProvider) => {
  if (!Array.isArray(prebuildQueryBlocks) || prebuildQueryBlocks.length === 0) {
    return [DEFAULT_QUERY_BLOCK()];
  }
  if (shouldNormalizeOperators(logProvider)) {
    return prebuildQueryBlocks.map(normalizeBlock);
  }
  return prebuildQueryBlocks;
};

const resolveMockOperators = (logProvider, operatorDescriptors) => {
  if (logProvider === 'service_map') return SERVICE_MAP_OPERATORS;
  if (logProvider === 'knowledge_graph') return KNOWLEDGE_GRAPH_OPERATORS;
  return getOperatorsForKind(operatorDescriptors, 'chip');
};

const resolveCombinedQuery = (queries) => {
  if (queries.length === 0) {
    return { combinedQuery: '', queryKeys: [''] };
  }
  if (queries.length === 1 && Array.isArray(queries[0].query)) {
    return { combinedQuery: queries[0].query, queryKeys: [queries[0].query_key] };
  }
  if (queries.length > 1 && queries.some((q) => Array.isArray(q.query))) {
    const queryBlock = queries.find((q) => Array.isArray(q.query));
    return queryBlock ? { combinedQuery: queryBlock.query, queryKeys: [queryBlock.query_key] } : { combinedQuery: [], queryKeys: [] };
  }
  const stringQueries = queries.filter((q) => typeof q.query === 'string');
  return {
    combinedQuery: stringQueries.length > 0 ? stringQueries.map((q) => q.query).join('; ') : '',
    queryKeys: stringQueries.length > 0 ? stringQueries.map((q) => q.query_key) : [''],
  };
};

const LogQueryBuilderAutocomplete = ({
  accountId,
  onQueryChange,
  queryItems,
  logProvider,
  operatorDescriptors,
  params,
  queryOperations = [],
  onQueryItemsChange,
  getLabelsFromProps = [],
  getLabelValuesFromProps = {},
  height = '10vh',
  width = '50%',
  allowMultipleQueries = true,
  deleteDataOnQueryBlockDeletion = (_query_key) => {},
  prebuildQueryBlocks,
  setPrebuildQueryBlocks,
  heading = 'Label',
  providerType = '',
  suggestionsMinWidth = 280,
}) => {
  // State for multiple query blocks
  const [queryBlocks, setQueryBlocks] = useState(() => initializeQueryBlocks(prebuildQueryBlocks, logProvider));
  const [activeBlockId, setActiveBlockId] = useState(0);
  const queryBlockIdCounter = useRef(1);
  const hasMultipleBlocks = useRef(false);

  const [inputValue, setInputValue] = useState('');
  const [suggestions, setSuggestions] = useState([]);
  const [showSuggestions, setShowSuggestions] = useState(false);
  const [isSuggestionsCapped, setIsSuggestionsCapped] = useState(false);
  const [selectedIndex, setSelectedIndex] = useState(-1);
  const [currentStep, setCurrentStep] = useState('label');
  const [pendingChip, setPendingChip] = useState({ label: '', operator: '', value: '' });
  const [labels, setLabels] = useState([]);
  const [values, setValues] = useState({});
  const [loadingValues, setLoadingValues] = useState(false);
  const [loadingLabels, setLoadingLabels] = useState(false);

  const [metricsList, setMetricsList] = useState([]);
  const [isMetricsLoading, setIsMetricsLoading] = useState(false);

  // Query block management
  const addQueryBlock = () => {
    hasMultipleBlocks.current = true; // Mark that we've entered multi-block mode
    const newBlock = {
      id: queryBlockIdCounter.current++,
      query_key: uuidv4(),
      selectedMetric: '',
      queryItems: [],
      operations: [],
      aggregator: 'avg',
      aggregatorBy: [],
    };
    setQueryBlocks([...queryBlocks, newBlock]);
    if (setPrebuildQueryBlocks) {
      setPrebuildQueryBlocks([...queryBlocks, newBlock]);
    }
    setActiveBlockId(newBlock.id);
  };

  const removeQueryBlock = (blockId) => {
    if (queryBlocks.length === 1) {
      snackbar.warning('Cannot remove the last query block');
      return;
    }
    const deletedBlock = queryBlocks.find((block) => block.id === blockId);
    deleteDataOnQueryBlockDeletion(deletedBlock.query_key);
    const updatedBlocks = queryBlocks.filter((block) => block.id !== blockId);
    setQueryBlocks(updatedBlocks);
    if (setPrebuildQueryBlocks) {
      setPrebuildQueryBlocks(updatedBlocks);
    }
    // Keep hasMultipleBlocks true to prevent props from overwriting user data
    // Once user has used multiple blocks, we should never sync from props again
    if (activeBlockId === blockId) {
      setActiveBlockId(updatedBlocks[0].id);
    }
  };

  useEffect(() => {
    if (setPrebuildQueryBlocks) {
      setPrebuildQueryBlocks(queryBlocks);
    }
  }, [queryBlocks, setPrebuildQueryBlocks]);

  // Mirror the externally-controlled selectedMetric (e.g. integration-configured
  // default ES index seeded by QueryModeSwitcher) into the internal block state
  // so derived values like activeSelectedMetric pick it up. Only runs while we
  // are still in single-block mode.
  useEffect(() => {
    if (!prebuildQueryBlocks || hasMultipleBlocks.current) return;
    const incoming = prebuildQueryBlocks[0]?.selectedMetric ?? '';
    setQueryBlocks((prev) => {
      if (prev[0]?.selectedMetric === incoming) return prev;
      return prev.map((b, i) => (i === 0 ? { ...b, selectedMetric: incoming } : b));
    });
  }, [prebuildQueryBlocks]);

  const updateQueryBlock = (blockId, updates) => {
    setQueryBlocks((blocks) => blocks.map((block) => (block.id === blockId ? { ...block, ...updates } : block)));
  };

  const getActiveBlock = () => {
    return queryBlocks.find((block) => block.id === activeBlockId) || queryBlocks[0];
  };

  const addOperation = () => {
    const activeBlock = getActiveBlock();
    const defaultOp = getLineOperators(operatorDescriptors)[0]?.value ?? '';
    const newOperations = [...activeBlock.operations, { id: Date.now(), op: defaultOp, value: '' }];
    updateQueryBlock(activeBlock.id, { operations: newOperations });
  };

  const updateOperation = (id, field, newValue) => {
    const activeBlock = getActiveBlock();
    const newOperations = activeBlock.operations.map((op) => (op.id === id ? { ...op, [field]: newValue } : op));
    updateQueryBlock(activeBlock.id, { operations: newOperations });
  };

  const deleteOperation = (id) => {
    const activeBlock = getActiveBlock();
    const newOperations = activeBlock.operations.filter((op) => op.id !== id);
    updateQueryBlock(activeBlock.id, { operations: newOperations });
  };

  const MAX_SUGGESTIONS = 100;
  const DEBOUNCE_DELAY = 150;

  const inputRef = useRef(null);
  const suggestionsRef = useRef(null);
  const chipIdCounter = useRef(0);
  const prevPairRef = useRef({ accountId: null, logProvider: null });
  const debounceTimerRef = useRef(null);

  const resetStates = () => {
    setInputValue('');
    setSuggestions([]);
    setShowSuggestions(false);
    setIsSuggestionsCapped(false);
    setSelectedIndex(-1);
    setCurrentStep('label');
    setPendingChip({ label: '', operator: '', value: '' });
    setLabels([]);
    setValues([]);
    setLoadingValues(false);
    setLoadingLabels(false);
    setMetricsList([]);
    setIsMetricsLoading(false);
    if (onQueryChange) {
      onQueryChange({ query: '', queryKeys: [''] });
    }
  };

  useEffect(() => {
    resetStates();
  }, [accountId]);

  // Cleanup debounce timer on unmount
  useEffect(() => {
    return () => {
      if (debounceTimerRef.current) clearTimeout(debounceTimerRef.current);
    };
  }, []);

  useEffect(() => {
    if (
      (logProvider == 'prometheus' ||
        logProvider == 'datadog' ||
        logProvider == 'newrelic' ||
        logProvider == 'dynatrace' ||
        logProvider == 'solarwinds') &&
      providerType == 'metrics'
    ) {
      const fetchMetrics = async () => {
        setIsMetricsLoading(true);
        try {
          const cachedPrometheusLabels = cache.getWithSuffix(`${accountId}.${logProvider}.labels`, null, {});
          if (cachedPrometheusLabels) {
            setMetricsList(cachedPrometheusLabels);
          } else {
            setMetricsList([]);
            const res = await observability.metricsList(accountId);
            if (res?.errors) {
              snackbar.error(`failed to fetch labels- ${parseHttpResponseBodyMessage(res?.data)}`);
              return;
            }
            const metricsList = res?.data?.data?.metrics_list?.map((m) => m.metric) || [];
            if (metricsList.length) {
              cache.setWithSuffix(`${accountId}.${logProvider}.labels`, metricsList, {}, 60 * 60 * 6);
              setMetricsList(metricsList);
            }
          }
        } catch (err) {
          snackbar.error(`Unexpected error fetching metrics: ${String(err)}`);
        } finally {
          setIsMetricsLoading(false);
        }
      };
      fetchMetrics();
    } else if (logProvider == 'ES' && providerType == 'metrics') {
      const fetchIndexes = async () => {
        setIsMetricsLoading(true);
        try {
          const cachedESIndexes = cache.getWithSuffix(`${accountId}.es.metrics.indexes`, null, {});
          if (cachedESIndexes) {
            setMetricsList(cachedESIndexes);
          } else {
            setMetricsList([]);
            const res = await observability.metricsList(accountId);
            if (res?.errors) {
              snackbar.error(`failed to fetch indices- ${parseHttpResponseBodyMessage(res?.data)}`);
              return;
            }
            const indexList = res?.data?.data?.metrics_list?.map((m) => m.metric) || [];
            if (indexList.length) {
              cache.setWithSuffix(`${accountId}.es.metrics.indexes`, indexList, {}, 60 * 60 * 6);
              setMetricsList(indexList);
            }
          }
        } catch (err) {
          snackbar.error(`Unexpected error fetching indices: ${String(err)}`);
        } finally {
          setIsMetricsLoading(false);
        }
      };
      fetchIndexes();
    } else if (logProvider == 'ES') {
      const fetchIndexes = async () => {
        setIsMetricsLoading(true);
        try {
          const cachedESIndexes = cache.getWithSuffix(`${accountId}.es.indexes`, null, {});
          if (cachedESIndexes) {
            setMetricsList(cachedESIndexes);
          } else {
            setMetricsList([]);
            const res = await observability.fetchLogLabels({
              account_id: accountId,
            });
            if (res?.errors) {
              snackbar.error(`failed to fetch labels- ${parseHttpResponseBodyMessage(res?.data)}`);
              return;
            }
            const indexList = res?.data?.data?.logs_list_labels?.map((m) => m.label) || [];
            if (indexList.length) {
              cache.setWithSuffix(`${accountId}.es.indexes`, indexList, {}, 60 * 60 * 6);
              setMetricsList(indexList);
            }
          }
        } catch (err) {
          snackbar.error(`Unexpected error fetching metrics: ${String(err)}`);
        } finally {
          setIsMetricsLoading(false);
        }
      };
      fetchIndexes();
    }
  }, [logProvider, accountId]);

  const activeSelectedMetric = queryBlocks.find((b) => b.id === activeBlockId)?.selectedMetric || queryBlocks[0]?.selectedMetric || '';

  useEffect(() => {
    if (!activeSelectedMetric) return;
    const fetchLabels = async () => {
      if (
        (logProvider === 'prometheus' ||
          logProvider == 'datadog' ||
          logProvider == 'newrelic' ||
          logProvider == 'dynatrace' ||
          logProvider == 'solarwinds') &&
        providerType == 'metrics'
      ) {
        try {
          setLoadingLabels(true);
          const response = await observability.metricsLabelList(accountId, activeSelectedMetric);
          if (response?.data?.errors) {
            snackbar.error(`Failed to fetch labels - ${response?.data?.error_msg || parseHttpResponseBodyMessage(response?.data)}`);
            return;
          }
          const responseAttributes = (response?.data?.data?.metrics_list_labels || []).filter((l) => l !== '__name__');
          if (responseAttributes.length > 0) {
            setLabels(responseAttributes);
          }
        } catch (err) {
          snackbar.error(`Failed to fetch labels - ${err.message}`);
        } finally {
          setLoadingLabels(false);
        }
      } else if (logProvider == 'ES' && providerType == 'metrics') {
        try {
          setLoadingLabels(true);
          const response = await observability.metricsLabelList(accountId, activeSelectedMetric);
          if (response?.data?.errors) {
            snackbar.error(`Failed to fetch labels - ${response?.data?.error_msg || parseHttpResponseBodyMessage(response?.data)}`);
            return;
          }
          const responseAttributes = response?.data?.data?.metrics_list_labels || [];
          if (responseAttributes.length > 0) {
            setLabels(responseAttributes);
          }
        } catch (err) {
          snackbar.error(`Failed to fetch labels - ${err.message}`);
        } finally {
          setLoadingLabels(false);
        }
      } else if (logProvider == 'ES') {
        try {
          setLoadingLabels(true);
          const response = await observability.logIndexFields(accountId, activeSelectedMetric);
          if (response?.data?.errors) {
            snackbar.error(`Failed to fetch index fields - ${response?.data?.error_msg || parseHttpResponseBodyMessage(response?.data)}`);
            return;
          }
          const responseAttributes = response?.data?.data?.logs_list_labels || [];
          if (responseAttributes.length > 0) {
            setLabels(responseAttributes);
          }
        } catch (err) {
          snackbar.error(`Failed to fetch labels - ${err.message}`);
        } finally {
          setLoadingLabels(false);
        }
      }
    };
    fetchLabels();
  }, [logProvider, activeBlockId, activeSelectedMetric]);

  useEffect(() => {
    if (
      !logProvider ||
      !accountId ||
      logProvider == 'service_map' ||
      logProvider == 'prometheus' ||
      logProvider == 'knowledge_graph' ||
      providerType == 'metrics'
    ) {
      return;
    }

    const prevPair = prevPairRef.current;
    const currPair = { accountId, logProvider };
    if (prevPair.accountId === accountId && prevPair.logProvider === logProvider) {
      return;
    }
    if (logProvider != 'loki' && logProvider != 'ES') {
      setLoadingLabels(true);
      observability
        .fetchLogLabels({
          account_id: accountId,
        })
        .then((res) => {
          const responseAttributes = res?.data?.data?.logs_list_labels || [];
          if (responseAttributes.length > 0) {
            setLabels(responseAttributes);
          }
        })
        .finally(() => {
          setLoadingLabels(false);
        });
    } else if (logProvider == 'loki') {
      setLoadingLabels(true);
      observability
        .fetchLogLabels({
          account_id: accountId,
          request: {
            query: `start=${params.startTime * 1000000}&end=${params.endTime * 1000000}`,
          },
        })
        .then((res) => {
          const responseAttributes = res?.data?.data?.logs_list_labels || [];
          if (responseAttributes.length > 0) {
            setLabels(responseAttributes);
          }
        })
        .finally(() => {
          setLoadingLabels(false);
        });
    }
    prevPairRef.current = currPair;
  }, [logProvider, accountId]);

  useEffect(() => {
    if (logProvider == 'service_map' || logProvider == 'knowledge_graph') {
      setLabels(getLabelsFromProps);
    }
  }, [getLabelsFromProps, logProvider]);

  const mockOperators = resolveMockOperators(logProvider, operatorDescriptors);

  // Fetch values for a specific label
  const fetchValuesForLabel = async (labelName) => {
    if (values[labelName]) {
      return values[labelName]; // Return cached values
    }

    setLoadingValues(true);
    try {
      const findLabel = labels.find((l) => (l.label || l.index || l.field) === labelName);
      let response = {};

      if (findLabel) {
        if (logProvider === 'service_map') {
          const labelVals = getLabelValuesFromProps[labelName];
          if (labelVals?.length > 0) {
            const labelValues = labelVals.map((e) => e.value);
            setValues((prev) => ({
              ...prev,
              [labelName]: labelValues,
            }));
            return labelValues;
          }
          return [];
        } else if (logProvider === 'knowledge_graph') {
          const res = await apiKubernetes1.knowledgeGraphFilterOptionLabelValues({
            filterType: heading == 'Attribute Filter' ? 'attribute' : 'label',
            filterKey: findLabel.label,
          });
          const labelValues = res?.data?.data?.kg_get_filter_values?.data?.values || [];
          setValues((prev) => ({
            ...prev,
            [findLabel.label]: labelValues,
          }));
          return labelValues;
        }
        if (logProvider === 'signoz') {
          response = await observability.fetchLogLabelValues({
            account_id: accountId,
            label_name: findLabel.label,
            request: {
              filterAttributeKeyDataType: findLabel?.attributes?.dataType || 'string',
              searchText: '',
              tagType: findLabel?.attributes?.type || 'resource',
            },
          });
        } else if (logProvider === 'loki') {
          response = await observability.fetchLogLabelValues({
            account_id: accountId,
            label_name: findLabel.label,
            request: {
              query: `start=${params.startTime * 1000000}&end=${params.endTime * 1000000}`,
            },
          });
        } else if (
          (logProvider === 'observe' ||
            logProvider === 'loggly' ||
            logProvider === 'azure_app_insights' ||
            logProvider == 'newrelic' ||
            logProvider == 'dynatrace' ||
            logProvider == 'pinot' ||
            logProvider == 'hive') &&
          providerType == 'logs'
        ) {
          response = await observability.fetchLogLabelValues({
            account_id: accountId,
            label_name: findLabel.label,
            request: {},
          });
        } else if (
          (logProvider === 'prometheus' ||
            logProvider === 'datadog' ||
            logProvider == 'newrelic' ||
            logProvider == 'dynatrace' ||
            logProvider == 'solarwinds') &&
          providerType == 'metrics'
        ) {
          const activeBlock = getActiveBlock();
          response = await observability.metricsLabelValueList(accountId, findLabel.label, {
            metric_name: activeBlock.selectedMetric,
          });
        } else if (logProvider === 'ES' && providerType === 'metrics') {
          const activeBlock = getActiveBlock();
          response = await observability.metricsLabelValueList(accountId, findLabel.label, {
            metric_name: activeBlock.selectedMetric,
          });
        } else if (logProvider === 'ES') {
          const activeBlock = getActiveBlock();
          response = await observability.fetchLogLabelValues({
            account_id: accountId,
            label_name: findLabel.label,
            request: {
              index: activeBlock.selectedMetric,
            },
          });
        }
        const data = response?.data?.data || {};
        const labelValues = data.logs_list_label_values?.map((d) => d.value) || data.metrics_list_label_values?.map((d) => d.value) || [];

        if (labelValues.length > 0) {
          // Cache the values
          setValues((prev) => ({
            ...prev,
            [labelName]: labelValues,
          }));
        }

        return labelValues;
      }
    } catch (error) {
      console.error('Error fetching values for label:', labelName, error);
      snackbar.error(`Failed to fetch values for ${labelName} - ${error.message}`);
    } finally {
      setLoadingValues(false);
    }

    return [];
  };

  const getAvailableLabels = () => {
    // Allow all labels to be used multiple times
    // If wanted to filter already selected label const usedLabels = new Set(chips.map((chip) => chip.label));
    return labels.map((g) => g.label || g.field);
  };

  const filterSuggestions = (suggestions, filter) => {
    if (!filter) {
      return suggestions;
    }
    return suggestions.filter(
      (suggestion) =>
        suggestion.value.toLowerCase().includes(filter.toLowerCase()) ||
        (suggestion.label && suggestion.label.toLowerCase().includes(filter.toLowerCase()))
    );
  };

  const getDisplayValue = () => {
    return inputValue;
  };

  const processSuggestions = async (value) => {
    const parts = value.trim().split(/\s+/);

    if (parts.length === 1) {
      // Only one word - could be label
      setCurrentStep('label');
      setPendingChip({ label: '', operator: '', value: '' });

      // Check if the typed label exists in available labels
      const availableLabels = getAvailableLabels();
      const matchingLabel = availableLabels.find((label) => label.toLowerCase() === parts[0].toLowerCase());

      if (matchingLabel) {
        // Exact match found - show operators immediately
        setPendingChip({ label: matchingLabel, operator: '', value: '' });
        setCurrentStep('operator');

        const operatorSuggestions = mockOperators.map((op) => ({
          type: 'operator',
          value: op.value,
          label: op.label,
          description: op.description,
        }));
        setIsSuggestionsCapped(false);
        setSuggestions(operatorSuggestions);
        setShowSuggestions(true);
        setSelectedIndex(-1);
        return;
      }

      // Show label suggestions (capped at MAX_SUGGESTIONS)
      const allLabels = getAvailableLabels().map((label) => ({ type: 'label', value: label, label }));
      const allFiltered = filterSuggestions(allLabels, parts[0]);
      const filtered = allFiltered.slice(0, MAX_SUGGESTIONS);
      setIsSuggestionsCapped(allFiltered.length > MAX_SUGGESTIONS);
      setSuggestions(filtered);
      setShowSuggestions(filtered.length > 0);
      setSelectedIndex(-1);
    } else if (parts.length === 2) {
      // Two parts - label + operator (or partial operator)
      const [label, operatorPart] = parts;
      setCurrentStep('operator');
      setPendingChip({ label, operator: '', value: '' });

      // Match against display label, backend token, and normalized legacy
      // value so typing `=` / `CONTAINS` / `_eq` all resolve to the same entry.
      const normalizedPart = normalizeLegacyOperator(operatorPart).toLowerCase();
      const matchingOperator = mockOperators.find(
        (op) =>
          op.value.toLowerCase() === operatorPart.toLowerCase() ||
          op.value.toLowerCase() === normalizedPart ||
          (op.label && op.label.toLowerCase() === operatorPart.toLowerCase())
      );

      if (matchingOperator) {
        // Value-less operators (exists / is null) create the chip immediately.
        if (
          matchingOperator.value === '_has_key' ||
          matchingOperator.value === '_is_null' ||
          matchingOperator.value === 'exists' ||
          matchingOperator.value === '!exists'
        ) {
          const activeBlock = getActiveBlock();
          const newChip = {
            label,
            operator: matchingOperator.value,
            value: '',
            id: chipIdCounter.current++,
          };
          const updatedChips = [...activeBlock.queryItems, newChip];
          updateQueryBlock(activeBlock.id, { queryItems: updatedChips });
          if (onQueryItemsChange && queryBlocks.length === 1) {
            onQueryItemsChange(updatedChips);
          }
          setInputValue('');
          setPendingChip({ label: '', operator: '', value: '' });
          setCurrentStep('label');
          setSuggestions([]);
          setShowSuggestions(false);
          setSelectedIndex(-1);
          return;
        }

        // Complete operator found, move to value step and fetch values
        setPendingChip({ label, operator: matchingOperator.value, value: '' });
        setCurrentStep('value');

        // Fetch and show value suggestions (capped at MAX_SUGGESTIONS)
        try {
          const labelValues = await fetchValuesForLabel(label);
          const allValueSuggestions = labelValues.map((val) => ({
            type: 'value',
            value: val,
            label: val,
          }));
          setIsSuggestionsCapped(allValueSuggestions.length > MAX_SUGGESTIONS);
          setSuggestions(allValueSuggestions.slice(0, MAX_SUGGESTIONS));
          setShowSuggestions(allValueSuggestions.length > 0);
        } catch {
          setIsSuggestionsCapped(false);
          setSuggestions([]);
          setShowSuggestions(false);
        }
        setSelectedIndex(-1);
        return;
      }

      // Show operator suggestions
      const operatorSuggestions = mockOperators.map((op) => ({
        type: 'operator',
        value: op.value,
        label: op.label,
        description: op.description,
      }));
      const filtered = filterSuggestions(operatorSuggestions, operatorPart);
      setIsSuggestionsCapped(false);
      setSuggestions(filtered);
      setShowSuggestions(filtered.length > 0);
      setSelectedIndex(-1);
    } else if (parts.length >= 3) {
      // Three or more parts - label + operator + value
      const [label, operatorPart, ...valueParts] = parts;
      const valueStr = valueParts.join(' ');
      setCurrentStep('value');
      // Normalize the typed operator so pendingChip always carries the backend
      // token, regardless of whether the user typed `=`, `_eq`, or `CONTAINS`.
      const normalizedPart = normalizeLegacyOperator(operatorPart);
      const matchedOp = mockOperators.find((op) => op.value === operatorPart || op.value === normalizedPart || op.label === operatorPart);
      setPendingChip({ label, operator: matchedOp?.value ?? operatorPart, value: valueStr });

      // Show filtered value suggestions based on current input (capped at MAX_SUGGESTIONS)
      if (values[label]) {
        const valueSuggestions = values[label].map((val) => ({
          type: 'value',
          value: val,
          label: val,
        }));
        const allFiltered = filterSuggestions(valueSuggestions, valueStr);
        setIsSuggestionsCapped(allFiltered.length > MAX_SUGGESTIONS);
        setSuggestions(allFiltered.slice(0, MAX_SUGGESTIONS));
        setShowSuggestions(allFiltered.length > 0);
        setSelectedIndex(-1);
      }
    }
  };

  const handleInputChange = (e) => {
    const value = e.target.value;

    // Check if user manually deleted everything - reset to label step
    if (!value.trim()) {
      if (debounceTimerRef.current) clearTimeout(debounceTimerRef.current);
      setInputValue('');
      setPendingChip({ label: '', operator: '', value: '' });
      setCurrentStep('label');
      setSuggestions([]);
      setShowSuggestions(false);
      setIsSuggestionsCapped(false);
      setSelectedIndex(-1);
      return;
    }

    // Update input value immediately for responsive typing
    setInputValue(value);

    // Debounce the suggestion filtering
    if (debounceTimerRef.current) clearTimeout(debounceTimerRef.current);
    debounceTimerRef.current = setTimeout(() => {
      processSuggestions(value);
    }, DEBOUNCE_DELAY);
  };

  const handleInputFocus = async () => {
    // Only show label suggestions if the input is
    // completely empty, to start the "guided click" flow.
    if (!inputValue.trim()) {
      const availableLabels = getAvailableLabels();
      const allLabels = availableLabels.slice(0, MAX_SUGGESTIONS).map((label) => ({ type: 'label', value: label, label }));
      setIsSuggestionsCapped(availableLabels.length > MAX_SUGGESTIONS);
      setSuggestions(allLabels);
      setShowSuggestions(allLabels.length > 0);
      setSelectedIndex(-1);
      setCurrentStep('label'); // Ensure we're at the start
    }
    // If the input is NOT empty, do nothing.
    // This allows the user to click into their freely-typed
    // text to edit it without triggering suggestions.
  };

  // Clear input state when switching blocks
  useEffect(() => {
    setInputValue('');
    setPendingChip({ label: '', operator: '', value: '' });
    setCurrentStep('label');
    setShowSuggestions(false);
    setSuggestions([]);
    setSelectedIndex(-1);
  }, [activeBlockId]);

  // Sync props data to first block (for backward compatibility) - ONLY when single block
  // Once we've entered multi-block mode, stop syncing from props entirely
  // Use deep comparison to prevent unnecessary updates when array references change but content is same
  useEffect(() => {
    if (queryItems && queryBlocks.length === 1 && !hasMultipleBlocks.current) {
      const normalizedIncoming = shouldNormalizeOperators(logProvider) ? queryItems.map(normalizeChip) : queryItems;
      const currentItems = queryBlocks[0].queryItems;
      if (JSON.stringify(currentItems) !== JSON.stringify(normalizedIncoming)) {
        updateQueryBlock(queryBlocks[0].id, { queryItems: normalizedIncoming });
      }
    }
  }, [queryItems, queryBlocks, logProvider]);

  useEffect(() => {
    if (queryOperations?.length && queryBlocks.length === 1 && !hasMultipleBlocks.current) {
      const normalizedIncoming = shouldNormalizeOperators(logProvider) ? queryOperations.map(normalizeOperation) : queryOperations;
      const currentOperations = queryBlocks[0].operations;
      if (JSON.stringify(currentOperations) !== JSON.stringify(normalizedIncoming)) {
        updateQueryBlock(queryBlocks[0].id, { operations: normalizedIncoming });
      }
    }
  }, [queryOperations, queryBlocks, logProvider]);

  // Generate combined query from all blocks
  useEffect(() => {
    const queries = queryBlocks
      .map((block) => {
        const query = generateQuery(
          logProvider,
          block.queryItems,
          block.operations,
          block.selectedMetric,
          block.aggregator,
          block.aggregatorBy,
          providerType
        );

        // Handle array-based queries
        if (Array.isArray(query)) {
          return query.length > 0 ? { query: query, query_key: block.query_key } : null;
        }

        // Handle string-based queries - ensure we have a valid string
        if (query && typeof query === 'string') {
          const trimmed = query.trim();
          return trimmed.length > 0 ? { query: trimmed, query_key: block.query_key } : null;
        }

        return null;
      })
      .filter((q) => q != null && q.query !== '');

    const { combinedQuery, queryKeys } = resolveCombinedQuery(queries);

    // For ES logs, include the selected index from the active block
    const activeBlock = queryBlocks.find((b) => b.id === activeBlockId) || queryBlocks[0];
    const selectedIndex = activeBlock?.selectedMetric || '';

    // For SolarWinds metrics, build SWO filter expression from chips and pass alongside the query
    let solarwindsRequest;
    if (logProvider === 'solarwinds' && providerType === 'metrics') {
      const filterParts = queryBlocks.flatMap((block) =>
        block.queryItems
          .filter((chip) => normalizeLegacyOperator(chip.operator) === '_eq' && chip.value)
          .map((chip) => `${chip.label}: [${chip.value}]`)
      );
      if (filterParts.length > 0) {
        solarwindsRequest = { filter: filterParts.join(' ') };
      }
    }

    if (onQueryChange) {
      onQueryChange({ query: combinedQuery, queryKeys: queryKeys, index: selectedIndex, solarwindsRequest });
    }
  }, [JSON.stringify(queryBlocks), logProvider]);

  const handleKeyDown = (e) => {
    // Handle Enter key to create chip from free text input
    if (e.key === 'Enter') {
      e.preventDefault();

      if (!inputValue.trim()) {
        return;
      }

      const parts = inputValue.trim().split(/\s+/);

      if (parts.length >= 3) {
        // Complete query: label operator value
        const [label, operatorPart, ...valueParts] = parts;
        const value = valueParts.join(' ');

        // Accept either the display label (=, contains), the backend token
        // (_eq), or a legacy value (CONTAINS). Store the backend token.
        const normalizedPart = normalizeLegacyOperator(operatorPart);
        const validOperator = mockOperators.find((op) => op.value === operatorPart || op.value === normalizedPart || op.label === operatorPart);
        if (validOperator) {
          const activeBlock = getActiveBlock();
          const newChip = { label, operator: validOperator.value, value, id: chipIdCounter.current++ };
          const updatedChips = [...activeBlock.queryItems, newChip];

          updateQueryBlock(activeBlock.id, { queryItems: updatedChips });
          // Only call onQueryItemsChange for backward compatibility when single block
          if (onQueryItemsChange && queryBlocks.length === 1) {
            onQueryItemsChange(updatedChips);
          }
          setInputValue('');
          setPendingChip({ label: '', operator: '', value: '' });
          setCurrentStep('label');
          setShowSuggestions(false);

          setTimeout(() => {
            inputRef.current?.focus();
          }, 0);
          return;
        }
      }

      // If there are suggestions visible, select the first one
      if (showSuggestions && suggestions.length > 0) {
        const suggestionToSelect = selectedIndex >= 0 ? suggestions[selectedIndex] : suggestions[0];
        handleSuggestionSelect(suggestionToSelect);
        return;
      }
    }

    if (!showSuggestions) {
      return;
    }

    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setSelectedIndex((prev) => (prev < suggestions.length - 1 ? prev + 1 : 0));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setSelectedIndex((prev) => (prev > 0 ? prev - 1 : suggestions.length - 1));
    } else if (e.key === 'Tab') {
      e.preventDefault();
      if (selectedIndex >= 0) {
        handleSuggestionSelect(suggestions[selectedIndex]);
      }
    } else if (e.key === 'Escape') {
      setShowSuggestions(false);
      setSelectedIndex(-1);
    }
  };

  const handleSuggestionSelect = async (suggestion) => {
    setShowSuggestions(false);
    setSelectedIndex(-1);

    if (suggestion.type === 'label') {
      setPendingChip({ label: suggestion.value, operator: '', value: '' });
      setInputValue(suggestion.value + ' ');
      setCurrentStep('operator');

      // Show operator suggestions immediately
      setTimeout(() => {
        const operatorSuggestions = mockOperators.map((op) => ({
          type: 'operator',
          value: op.value,
          label: op.label,
          description: op.description,
        }));
        setIsSuggestionsCapped(false);
        setSuggestions(operatorSuggestions);
        setShowSuggestions(true);
        setSelectedIndex(-1);
      }, 0);
    } else if (suggestion.type === 'operator') {
      // Value-less operators (exists / is null) create the chip immediately.
      if (suggestion.value === '_has_key' || suggestion.value === '_is_null' || suggestion.value === 'exists' || suggestion.value === '!exists') {
        const activeBlock = getActiveBlock();
        const newChip = {
          label: pendingChip.label,
          operator: suggestion.value,
          value: '',
          id: chipIdCounter.current++,
        };
        const updatedChips = [...activeBlock.queryItems, newChip];
        updateQueryBlock(activeBlock.id, { queryItems: updatedChips });
        if (onQueryItemsChange && queryBlocks.length === 1) {
          onQueryItemsChange(updatedChips);
        }
        setInputValue('');
        setPendingChip({ label: '', operator: '', value: '' });
        setCurrentStep('label');
        setSuggestions([]);
        setShowSuggestions(false);
        setSelectedIndex(-1);
        return;
      }

      // Input shows the friendly label (=, contains, ...) while pendingChip
      // holds the backend token so the eventually-created chip is correct.
      const operatorDisplay = suggestion.label ?? suggestion.value;
      const newInputValue = pendingChip.label + ' ' + operatorDisplay + ' ';
      setPendingChip({ ...pendingChip, operator: suggestion.value });
      setInputValue(newInputValue);
      setCurrentStep('value');

      // Fetch and show value suggestions (capped at MAX_SUGGESTIONS)
      try {
        const labelValues = await fetchValuesForLabel(pendingChip.label);
        const allValueSuggestions = labelValues.map((val) => ({
          type: 'value',
          value: val,
          label: val,
        }));
        setIsSuggestionsCapped(allValueSuggestions.length > MAX_SUGGESTIONS);
        setSuggestions(allValueSuggestions.slice(0, MAX_SUGGESTIONS));
        setShowSuggestions(allValueSuggestions.length > 0);
        setSelectedIndex(-1);
      } catch {
        setIsSuggestionsCapped(false);
        setSuggestions([]);
        setShowSuggestions(false);
      }

      // Set cursor to the end of the input
      setTimeout(() => {
        if (inputRef.current) {
          inputRef.current.focus();
          inputRef.current.setSelectionRange(newInputValue.length, newInputValue.length);
        }
      }, 0);
      return;
    } else if (suggestion.type === 'value') {
      // When value is selected, immediately create a chip
      const activeBlock = getActiveBlock();
      const newChip = {
        label: pendingChip.label,
        operator: pendingChip.operator,
        value: suggestion.value,
        id: chipIdCounter.current++,
      };
      const updatedChips = [...activeBlock.queryItems, newChip];

      updateQueryBlock(activeBlock.id, { queryItems: updatedChips });
      // Only call onQueryItemsChange for backward compatibility when single block
      if (onQueryItemsChange && queryBlocks.length === 1) {
        onQueryItemsChange(updatedChips);
      }
      setInputValue('');
      setPendingChip({ label: '', operator: '', value: '' });
      setCurrentStep('label');
      setSuggestions([]);
      setShowSuggestions(false);
      setSelectedIndex(-1);
      return;
    }

    setTimeout(() => {
      inputRef.current?.focus();
    }, 0);
  };

  const handleChipDelete = (block, chipId) => {
    deleteDataOnQueryBlockDeletion(block.query_key);
    const updated = block.queryItems.filter((chip) => chip.id !== chipId);
    updateQueryBlock(block.id, { queryItems: updated });
    if (updated.length === 0 && queryBlocks.length === 1) {
      updateQueryBlock(block.id, { operations: [] });
    }
    // Only call onQueryItemsChange for backward compatibility when single block
    if (onQueryItemsChange && queryBlocks.length === 1) {
      onQueryItemsChange(updated);
    }
  };

  const getPlaceholder = () => {
    switch (currentStep) {
      case 'label':
        return 'Type label name or select from suggestions.';
      case 'operator':
        return `Type operator for "${pendingChip.label}" or select from suggestions.`;
      case 'value':
        return `Type value for "${pendingChip.label} ${pendingChip.operator}" or select from suggestions.`;
      default:
        return 'Type query (e.g., "service.name = my-api") or use suggestions.';
    }
  };

  const getStepIcon = () => {
    if ((currentStep === 'value' && loadingValues) || (currentStep === 'label' && loadingLabels)) {
      return <CircularProgress size={20} />;
    }

    switch (currentStep) {
      case 'label':
        return <LabelIcon sx={{ fontSize: '20px' }} color='success' />;
      case 'operator':
        return <FilterIcon sx={{ fontSize: '20px' }} color='warning' />;
      case 'value':
        return <ValueIcon sx={{ fontSize: '20px' }} color='success' />;
      default:
        return <SearchIcon sx={{ fontSize: '20px' }} color='success' />;
    }
  };

  useEffect(() => {
    const handleClickOutside = (event) => {
      const isInsideSuggestions = suggestionsRef.current?.contains(event.target);
      const isInsideInput = inputRef.current?.contains(event.target);

      if (!isInsideSuggestions && !isInsideInput) {
        setShowSuggestions(false);
      }
    };

    document.addEventListener('mousedown', handleClickOutside, true);
    return () => document.removeEventListener('mousedown', handleClickOutside, true);
  }, []);

  const activeBlock = getActiveBlock();

  return (
    <Box sx={{ width: '100%', minHeight: height, display: 'flex', flexDirection: 'column', gap: 2 }}>
      {/* Query Blocks */}
      {queryBlocks.map((block, index) => (
        <Box key={block.id} sx={{ display: 'flex', flexDirection: 'row', gap: 2, width: '100%' }}>
          {/* Input Field with Info Icon */}
          <Box
            sx={{
              position: 'relative',
              display: 'flex',
              alignItems: 'flex-start',
              flexDirection: 'column',
              width: width,
              gap: '8px',
              marginTop: '6px',
              padding: '12px 16px',
              border: activeBlockId === block.id ? `1px solid ${colors.border.secondaryLightest}` : '1px solid #E5E7EB',
              borderRadius: '6px',
            }}
            onClick={() => setActiveBlockId(block.id)}
          >
            <Box display={'flex'} flexDirection={'row'} gap={'6px'} width={'90%'} alignItems={'center'}>
              {allowMultipleQueries && (
                <Typography variant='caption' sx={{ fontWeight: 600, color: colors.text.secondary }}>
                  Query {allowMultipleQueries ? index + 1 : ''}
                </Typography>
              )}
              {queryBlocks.length > 1 && (
                <IconButton
                  size='small'
                  onClick={(e) => {
                    e.stopPropagation();
                    removeQueryBlock(block.id);
                  }}
                  sx={{ ml: 'auto', color: colors.text.tertiary }}
                >
                  <CloseIcon sx={{ fontSize: '16px' }} />
                </IconButton>
              )}
            </Box>
            <Box display='flex' flexDirection='column' gap='8px' width='100%'>
              {/* Top Row */}
              <Box display='flex' flexDirection='row' gap='6px' width='100%'>
                {(((logProvider === 'prometheus' ||
                  logProvider === 'datadog' ||
                  logProvider === 'newrelic' ||
                  logProvider === 'dynatrace' ||
                  logProvider === 'solarwinds') &&
                  providerType === 'metrics') ||
                  logProvider === 'ES') && (
                  <FilterDropdownButton
                    key={`auto-complete-promql-${block.id}`}
                    label={`Select a ${logProvider === 'ES' ? 'Index' : 'Metric'}`}
                    value={block.selectedMetric || null}
                    options={metricsList ?? []}
                    // ES allows wildcard index patterns server-side (e.g.
                    // `fluentk8s-prod*`). Enable freeSolo for ES so users can
                    // type a pattern that isn't an exact match in the daily
                    // index list and select it as the value.
                    freeSolo={logProvider === 'ES'}
                    searchPlaceholder={logProvider === 'ES' ? 'Search or type pattern (use * for wildcard)...' : undefined}
                    disabled={logProvider !== 'ES' && metricsList?.length === 0}
                    onSelect={(_event, value) => {
                      updateQueryBlock(block.id, { selectedMetric: value, queryItems: [], operations: [] });
                      if (onQueryItemsChange && queryBlocks.length === 1) {
                        onQueryItemsChange([]);
                      }
                      setLabels([]);
                      setValues({});
                      setInputValue('');
                      setSuggestions([]);
                      setShowSuggestions(false);
                      setSelectedIndex(-1);
                      setCurrentStep('label');
                      setPendingChip({ label: '', operator: '', value: '' });
                    }}
                    isOptionsLoading={isMetricsLoading}
                  />
                )}

                <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, width: '90%' }}>
                  <Box sx={{ position: 'relative', flex: 1 }}>
                    <TextField
                      inputRef={activeBlockId === block.id ? inputRef : null}
                      fullWidth
                      title={logProvider === 'prometheus' && !block.selectedMetric ? 'Select a metric to enable label filter' : ''}
                      value={activeBlockId === block.id ? getDisplayValue() : ''}
                      onChange={activeBlockId === block.id ? handleInputChange : undefined}
                      onKeyDown={activeBlockId === block.id ? handleKeyDown : undefined}
                      onFocus={() => {
                        setActiveBlockId(block.id);
                        handleInputFocus();
                      }}
                      placeholder={activeBlockId === block.id ? getPlaceholder() : 'Click to edit'}
                      disabled={
                        loadingLabels ||
                        loadingValues ||
                        ((logProvider === 'prometheus' ||
                          logProvider == 'datadog' ||
                          logProvider == 'ES' ||
                          ((logProvider == 'dynatrace' || logProvider == 'solarwinds') && providerType === 'metrics')) &&
                          !block.selectedMetric)
                      }
                      variant='outlined'
                      InputProps={{
                        startAdornment: (
                          <InputAdornment position='start'>
                            <Typography
                              variant='subtitle2'
                              sx={{
                                fontSize: '12px',
                                mr: '4px',
                                fontWeight: 'medium',
                                color: colors.text.secondary,
                              }}
                            >
                              {heading}:
                            </Typography>
                            {activeBlockId === block.id && getStepIcon()}{' '}
                          </InputAdornment>
                        ),
                        sx: {
                          fontSize: '12px',
                          padding: '8px 12px',
                          height: '34px',
                        },
                      }}
                      sx={{
                        '& .MuiOutlinedInput-root': {
                          '& fieldset': {
                            borderColor: 'grey.300',
                          },
                          '&:hover fieldset': {
                            borderColor: colors.border.primary,
                          },
                          '&.Mui-focused fieldset': {
                            borderColor: colors.border.primary,
                          },
                        },
                      }}
                    />
                    {/* Suggestions Dropdown - portalled to body to avoid sidebar/parent clipping */}
                    <Popper
                      open={activeBlockId === block.id && showSuggestions && suggestions.length > 0}
                      anchorEl={activeBlockId === block.id ? inputRef.current : null}
                      placement='bottom-start'
                      style={{ zIndex: 1500 }}
                      modifiers={[
                        { name: 'flip', enabled: true },
                        { name: 'preventOverflow', enabled: true, options: { boundary: 'viewport', padding: 8 } },
                      ]}
                    >
                      <Paper
                        ref={suggestionsRef}
                        elevation={8}
                        sx={{
                          maxHeight: 300,
                          minWidth: inputRef.current ? Math.max(inputRef.current.offsetWidth, suggestionsMinWidth) : suggestionsMinWidth,
                          width: 'max-content',
                          maxWidth: '90vw',
                          display: 'flex',
                          flexDirection: 'column',
                          mt: '2px',
                        }}
                      >
                        <List disablePadding sx={{ overflowY: 'auto', overflowX: 'hidden', flex: 1 }}>
                          {suggestions.map((suggestion, index) => (
                            <ListItem key={`${suggestion.type}-${suggestion.value}`} disablePadding>
                              <ListItemButton
                                selected={index === selectedIndex}
                                onClick={() => handleSuggestionSelect(suggestion)}
                                onMouseEnter={() => setSelectedIndex(index)}
                                sx={{
                                  p: '8px 12px',
                                  borderBottom: index < suggestions.length - 1 ? 1 : 0,
                                  borderColor: 'divider',
                                  '&.Mui-selected': {
                                    bgcolor: '#F3F3F3',
                                    color: colors.text.secondary,
                                    borderLeft: 4,
                                    borderLeftColor: colors.border.primary,
                                  },
                                  '&:hover': {
                                    bgcolor: '#F3F3F3 !important',
                                    color: colors.text.secondary,
                                  },
                                }}
                              >
                                <Box sx={{ mr: 2 }}>
                                  {suggestion.type === 'label' && <LabelIcon sx={{ fontSize: '20px' }} color='success' />}
                                  {suggestion.type === 'operator' && <FilterIcon sx={{ fontSize: '20px' }} color='warning' />}
                                  {suggestion.type === 'value' && <ValueIcon sx={{ fontSize: '20px' }} color='success' />}
                                </Box>
                                <ListItemText
                                  primary={suggestion.label ?? suggestion.value}
                                  secondary={suggestion.description}
                                  sx={{
                                    padding: '0px',
                                  }}
                                  primaryTypographyProps={{
                                    fontSize: '12px',
                                    fontWeight: 'large',
                                  }}
                                  secondaryTypographyProps={{
                                    fontSize: '0.75rem',
                                  }}
                                />
                              </ListItemButton>
                            </ListItem>
                          ))}
                        </List>
                        {isSuggestionsCapped && (
                          <Box
                            sx={{
                              p: '6px 12px',
                              borderTop: `1px solid ${colors.border.tertiary || '#eee'}`,
                              backgroundColor: colors.background.primaryLightest || '#f5f5f5',
                              flexShrink: 0,
                            }}
                          >
                            <Typography sx={{ fontSize: '11px', color: colors.text.tertiary, textAlign: 'center' }}>
                              Showing first {MAX_SUGGESTIONS} results. Type to narrow down.
                            </Typography>
                          </Box>
                        )}
                      </Paper>
                    </Popper>
                  </Box>

                  {activeBlockId === block.id && (
                    <CustomTooltip
                      placement='bottom-start'
                      tooltipStyle={{ maxWidth: '360px', padding: '16px' }}
                      title={
                        <Box>
                          <Typography sx={{ fontSize: '13px', fontWeight: 600, color: colors.text.secondary, mb: 0 }}>Filter by {heading}</Typography>
                          <Typography sx={{ fontSize: '12px', color: colors.text.tertiary, mb: 1.5 }}>
                            {heading === 'Attribute'
                              ? 'Attributes are resource-level metadata (e.g. cluster, namespace, pod).'
                              : 'Labels classify nodes by type (e.g. service, host, pod).'}
                          </Typography>

                          {/* Guided flow steps */}
                          <Typography
                            sx={{
                              fontSize: '11px',
                              fontWeight: 600,
                              color: colors.text.tertiary,
                              mb: 0.5,
                              textTransform: 'uppercase',
                              letterSpacing: '0.5px',
                            }}
                          >
                            Guided Flow
                          </Typography>
                          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
                            {[
                              `Click the input and pick a ${heading.toLowerCase()} from the dropdown`,
                              'Choose an operator (=, !=, LIKE, etc.)',
                              `Select or type a value for that ${heading.toLowerCase()}`,
                              'Filter is applied automatically as a chip',
                            ].map((text, i) => (
                              <Box key={text} sx={{ display: 'flex', alignItems: 'flex-start', gap: 1 }}>
                                <Typography
                                  sx={{
                                    fontSize: '11px',
                                    fontWeight: 600,
                                    color: colors.primary,
                                    background: colors.background.primaryLightest,
                                    padding: '2px',
                                    borderRadius: '20px',
                                    width: '16px',
                                    minWidth: '16px',
                                    textAlign: 'center',
                                  }}
                                >
                                  {i + 1}
                                </Typography>
                                <Typography sx={{ fontSize: '12px', color: colors.text.secondary }}>{text}</Typography>
                              </Box>
                            ))}
                          </Box>

                          {/* Free-text shortcut */}
                          <Box
                            sx={{
                              mt: 1.5,
                              pt: 1.5,
                              borderTop: `1px solid ${colors.border.primaryLight}`,
                            }}
                          >
                            <Typography
                              sx={{
                                fontSize: '11px',
                                fontWeight: 600,
                                color: colors.text.tertiary,
                                mb: 0.5,
                                textTransform: 'uppercase',
                                letterSpacing: '0.5px',
                              }}
                            >
                              Quick Input
                            </Typography>
                            <Typography sx={{ fontSize: '12px', color: colors.text.secondary, mb: 0.5 }}>
                              Type the full filter directly and press <strong>Enter</strong>:
                            </Typography>
                            <Box
                              sx={{
                                backgroundColor: colors.background.primaryLightest,
                                borderRadius: '4px',
                                padding: '6px 10px',
                                fontFamily: 'monospace',
                                fontSize: '12px',
                                color: colors.text.secondary,
                              }}
                            >
                              service.name = my-api
                            </Box>
                          </Box>

                          {/* Keyboard shortcuts */}
                          <Box
                            sx={{
                              mt: 1.5,
                              pt: 1.5,
                              borderTop: `1px solid ${colors.border.primaryLight}`,
                            }}
                          >
                            <Typography
                              sx={{
                                fontSize: '11px',
                                fontWeight: 600,
                                color: colors.text.tertiary,
                                mb: 0.5,
                                textTransform: 'uppercase',
                                letterSpacing: '0.5px',
                              }}
                            >
                              Keyboard Shortcuts
                            </Typography>
                            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.5 }}>
                              {[
                                { key: '↑ ↓', desc: 'Navigate suggestions' },
                                { key: 'Tab', desc: 'Select highlighted suggestion' },
                                { key: 'Enter', desc: 'Confirm selection or apply filter' },
                                { key: 'Esc', desc: 'Dismiss suggestions' },
                              ].map(({ key, desc }) => (
                                <Box key={key} sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                                  <Typography
                                    component='span'
                                    sx={{
                                      fontSize: '11px',
                                      fontWeight: 600,
                                      fontFamily: 'monospace',
                                      color: colors.text.secondary,
                                      backgroundColor: colors.background.primaryLightest,
                                      border: `1px solid ${colors.border.primaryLight}`,
                                      borderRadius: '3px',
                                      padding: '1px 5px',
                                      minWidth: '32px',
                                      textAlign: 'center',
                                    }}
                                  >
                                    {key}
                                  </Typography>
                                  <Typography sx={{ fontSize: '12px', color: colors.text.secondary }}>{desc}</Typography>
                                </Box>
                              ))}
                            </Box>
                          </Box>

                          {/* Multiple filters tip */}
                          <Box
                            sx={{
                              mt: 1.5,
                              pt: 1.5,
                              borderTop: `1px solid ${colors.border.primaryLight}`,
                            }}
                          >
                            <Typography
                              sx={{
                                fontSize: '11px',
                                fontWeight: 600,
                                color: colors.text.tertiary,
                                mb: 0.5,
                                textTransform: 'uppercase',
                                letterSpacing: '0.5px',
                              }}
                            >
                              Tips
                            </Typography>
                            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.5 }}>
                              <Box sx={{ display: 'flex', alignItems: 'flex-start', gap: 1 }}>
                                <Typography sx={{ fontSize: '12px', color: colors.text.secondary }}>
                                  Add multiple filters to narrow results — all filters must match (AND)
                                </Typography>
                              </Box>
                              <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                                <CloseIcon sx={{ fontSize: '11px', color: colors.text.tertiary, minWidth: '14px' }} />
                                <Typography sx={{ fontSize: '12px', color: colors.text.secondary }}>
                                  Click × on any chip to remove that filter
                                </Typography>
                              </Box>
                            </Box>
                          </Box>
                        </Box>
                      }
                    >
                      <IconButton
                        sx={{
                          color: colors.gray,
                          opacity: 0.4,
                          padding: 0,
                        }}
                      >
                        <InfoIcon sx={{ fontSize: '16px' }} />
                      </IconButton>
                    </CustomTooltip>
                  )}
                </Box>
              </Box>
              {logProvider === 'datadog' && (
                <>
                  <FilterDropdownButton
                    key={`auto-complete-aggregator-${block.id}`}
                    label='Aggregator'
                    value={block.aggregator || null}
                    options={['avg', 'max', 'min', 'sum']}
                    disabled={metricsList?.length === 0}
                    onSelect={(_event, value) => {
                      updateQueryBlock(block.id, { aggregator: value });
                    }}
                  />
                  <FilterDropdownButton
                    key={`auto-complete-aggregatorby-${block.id}`}
                    label='Aggregator By'
                    value={block.aggregatorBy}
                    multiple
                    options={getAvailableLabels()}
                    disabled={labels?.length === 0}
                    onSelect={(_event, value) => {
                      updateQueryBlock(block.id, { aggregatorBy: value });
                    }}
                  />
                </>
              )}
            </Box>

            {block.queryItems.length > 0 && (
              <Box sx={{ mt: 1, width: '100%' }}>
                <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 1, alignItems: 'center' }}>
                  {block.queryItems.slice(0, 6).map((chip) => (
                    <Chip
                      key={chip.id}
                      label={`${chip.label} ${getOperatorDisplayLabel(chip.operator, operatorDescriptors)} ${chip.value} `}
                      title={`${chip.label} ${getOperatorDisplayLabel(chip.operator, operatorDescriptors)} ${chip.value}`}
                      onDelete={() => handleChipDelete(block, chip.id)}
                      deleteIcon={<CloseIcon sx={{ fontSize: '12px', width: '16px', height: '16px' }} />}
                      sx={{
                        padding: '4px 4px',
                        color: colors.text.secondary,
                        backgroundColor: colors.background.primaryLightest,
                        border: `1px solid ${colors.border.primaryLight}`,
                        height: 'auto',
                        maxWidth: '100%',
                        '& .MuiChip-label': {
                          fontSize: '12px',
                          whiteSpace: 'normal',
                          wordBreak: 'break-word',
                        },
                      }}
                    />
                  ))}
                  {block.queryItems.length > 6 && (
                    <Tooltip
                      title={
                        <Box
                          sx={{
                            maxHeight: '300px',
                            overflowY: 'auto',
                            overflowX: 'hidden',
                            '&::-webkit-scrollbar': {
                              width: '6px',
                            },
                            '&::-webkit-scrollbar-track': {
                              backgroundColor: 'rgba(255, 255, 255, 0.1)',
                              borderRadius: '3px',
                            },
                            '&::-webkit-scrollbar-thumb': {
                              backgroundColor: 'rgba(255, 255, 255, 0.3)',
                              borderRadius: '3px',
                              '&:hover': {
                                backgroundColor: 'rgba(255, 255, 255, 0.5)',
                              },
                            },
                          }}
                        >
                          {block.queryItems.slice(4).map((chip, chipIndex) => (
                            <Box
                              key={chip.id}
                              sx={{
                                display: 'flex',
                                alignItems: 'center',
                                justifyContent: 'space-between',
                                backgroundColor: 'rgba(255, 255, 255, 0.1)',
                                borderRadius: '4px',
                                padding: '4px 8px',
                                mb: chipIndex < block.queryItems.slice(2).length - 1 ? 0.5 : 0,
                                border: '1px solid rgba(255, 255, 255, 0.2)',
                              }}
                            >
                              <Typography
                                variant='caption'
                                sx={{
                                  fontFamily: 'monospace',
                                  fontSize: '0.75rem',
                                  flex: 1,
                                  wordBreak: 'break-word',
                                }}
                              >
                                {`${chip.label} ${getOperatorDisplayLabel(chip.operator, operatorDescriptors)} ${chip.value}`}
                              </Typography>
                              <IconButton
                                size='small'
                                onClick={(e) => {
                                  e.stopPropagation();
                                  handleChipDelete(block, chip.id);
                                }}
                                sx={{
                                  ml: 1,
                                  width: '16px',
                                  height: '16px',
                                  color: 'rgba(255, 255, 255, 0.7)',
                                  flexShrink: 0,
                                  '&:hover': {
                                    color: 'white',
                                    backgroundColor: 'rgba(255, 255, 255, 0.1)',
                                  },
                                }}
                              >
                                <CloseIcon sx={{ fontSize: '12px' }} />
                              </IconButton>
                            </Box>
                          ))}
                        </Box>
                      }
                      arrow
                      placement='top'
                      componentsProps={{
                        tooltip: {
                          sx: {
                            backgroundColor: 'rgba(97, 97, 97, 0.92)',
                            color: 'white',
                            maxWidth: '400px',
                            maxHeight: '350px',
                            padding: '12px',
                          },
                        },
                        popper: {
                          modifiers: [
                            {
                              name: 'preventOverflow',
                              options: {
                                boundary: 'viewport',
                                padding: 8,
                              },
                            },
                            {
                              name: 'flip',
                              options: {
                                fallbackPlacements: ['bottom', 'left', 'right'],
                              },
                            },
                          ],
                        },
                      }}
                    >
                      <Chip
                        label={`+${block.queryItems.length - 6}`}
                        color='secondary'
                        variant='outlined'
                        sx={{
                          fontFamily: 'monospace',
                          cursor: 'help',
                          '& .MuiChip-label': {
                            fontSize: '0.75rem',
                            fontWeight: 'bold',
                          },
                        }}
                      />
                    </Tooltip>
                  )}
                </Box>
              </Box>
            )}
          </Box>

          {/* Operations for Loki, New Relic, Dynatrace, and Loggly — logs only.
              Operations are line/log filters; the metrics screen never shows them, regardless of integration. */}
          {['loki', 'newrelic', 'dynatrace', 'loggly'].includes(logProvider) && providerType !== 'metrics' && activeBlockId === block.id && (
            <Box sx={{ width: '50%', padding: '12px 16px', border: '1px solid #E5E7EB', borderRadius: '6px', mt: '5px' }}>
              <Typography variant='subtitle2' sx={{ mb: 1, fontWeight: 'medium', color: colors.text.secondary }}>
                Operations
              </Typography>
              {block.operations.map((op) => (
                <Box key={op.id} sx={{ display: 'flex', alignItems: 'flex-start', gap: 1, height: '44px' }}>
                  <FilterDropdownButton
                    options={getLineOperators(operatorDescriptors)}
                    value={getLineOperators(operatorDescriptors).find((o) => (o.value ?? o) === op.op) || null}
                    label='Operation'
                    onSelect={(event) => {
                      updateOperation(op.id, 'op', event.target.value);
                    }}
                  />

                  <TextField
                    size='small'
                    placeholder='Enter value'
                    value={op.value}
                    onChange={(e) => updateOperation(op.id, 'value', e.target.value)}
                    sx={{
                      flex: 1,
                      height: '36px',
                      '& .MuiInputBase-input': {
                        height: '36px',
                        padding: '0 14px',
                        boxSizing: 'border-box',
                      },
                    }}
                  />

                  <DeleteButton onClick={() => deleteOperation(op.id)} />
                </Box>
              ))}

              <Box>
                <CustomButton
                  variant='tertiary'
                  size='Medium'
                  onClick={addOperation}
                  text={'+ Add Operation'}
                  disabled={block.queryItems.length === 0 && !(logProvider === 'dynatrace' && providerType === 'logs')}
                />
                {block.queryItems.length === 0 && !(logProvider === 'dynatrace' && providerType === 'logs') && (
                  <Box sx={{ display: 'flex', alignItems: 'center', mt: 1, gap: 0.5 }}>
                    <WarningIcon sx={{ fontSize: 16, color: 'warning.main' }} />
                    <Typography variant='caption' sx={{ color: colors.text.secondary }}>
                      Select at least 1 label filter (label and value)
                    </Typography>
                  </Box>
                )}
              </Box>
            </Box>
          )}
        </Box>
      ))}

      {/* Add Query Button */}
      {allowMultipleQueries && logProvider !== 'ES' && (
        <Box sx={{ display: 'flex', justifyContent: 'flex-start', mt: 2, flexDirection: 'column', gap: 1, alignItems: 'flex-start' }}>
          <CustomButton
            variant='primary'
            size='Medium'
            onClick={addQueryBlock}
            text={'Add Query'}
            startIcon={<AddIcon />}
            disabled={activeBlock.queryItems.length === 0 || (logProvider === 'prometheus' && !activeBlock.selectedMetric)}
          />
          {(activeBlock.queryItems.length === 0 || (logProvider === 'prometheus' && !activeBlock.selectedMetric)) && (
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
              <WarningIcon sx={{ fontSize: 16, color: 'warning.main' }} />
              <Typography variant='caption' sx={{ color: colors.text.secondary }}>
                {logProvider === 'prometheus' && !activeBlock.selectedMetric
                  ? 'Select a metric first, then add label filters to enable adding a new query'
                  : 'Add at least 1 label filter to enable adding a new query'}
              </Typography>
            </Box>
          )}
        </Box>
      )}
    </Box>
  );
};

export default LogQueryBuilderAutocomplete;
