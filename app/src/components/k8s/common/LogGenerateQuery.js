export const generateQuery = (logProvider, chips, operations, metricName = '', aggregator, aggregatorBy, providerType = '') => {
  const isLogsProvider =
    logProvider === 'loggly' ||
    logProvider === 'observe' ||
    logProvider === 'loki' ||
    logProvider === 'azure_app_insights' ||
    (logProvider === 'signoz' && providerType === 'logs') ||
    (logProvider === 'newrelic' && providerType === 'logs') ||
    (logProvider === 'dynatrace' && providerType === 'logs') ||
    logProvider === 'ES' ||
    (logProvider === 'solarwinds' && providerType === 'logs');

  const hasValidOperations = isLogsProvider && operations?.some((op) => op.value && op.value.trim() !== '');

  if (chips.length === 0 || (chips.length == 0 && metricName)) {
    if (logProvider == 'datadog') {
      const byClause = aggregatorBy?.length ? ` by {${aggregatorBy.join(',')}}` : '';
      return `${aggregator}:${metricName}{*} ${byClause}`;
    }
    // For log providers, operations alone (without label chips) can form a valid query
    if (!hasValidOperations) {
      return metricName || '';
    }
  }

  if (logProvider === 'service_map' || logProvider === 'knowledge_graph') {
    const filters = chips.map((chip) => {
      return {
        key: chip.label,
        value: chip.value,
        operator: chip.operator,
      };
    });
    return JSON.stringify(filters);
  } else if (logProvider === 'solarwinds' && providerType !== 'logs') {
    // SolarWinds: backend expects the metric name as the query value.
    // Label filters from chips are passed separately via req.Request["filter"]
    // (built in LogQueryBuilderAutocomplete and forwarded through onQueryChange).
    return metricName?.trim() || '';
  } else if (logProvider == 'dynatrace' && providerType !== 'logs') {
    // Generate Dynatrace Grail DQL timeseries query
    // Backend passes verbatim if query starts with "timeseries"
    const metricKey = metricName?.trim() || '';
    const aggregator = operations?.[0]?.op?.toLowerCase() || 'avg';
    let dql = `timeseries val = ${aggregator}(${metricKey})`;
    if (chips.length > 0) {
      const filterExprs = chips.map((chip) => {
        const escapedLabel = chip.label.replace(/`/g, '\\`');
        const escapedValue = chip.value.replace(/"/g, '\\"');
        switch (chip.operator) {
          case '!=':
          case '_neq':
            return `\`${escapedLabel}\` != "${escapedValue}"`;
          case '=~':
          case '_regex':
            return `\`${escapedLabel}\` matches "${escapedValue}"`;
          case '!~':
          case '_nregex':
            return `not (\`${escapedLabel}\` matches "${escapedValue}")`;
          default:
            return `\`${escapedLabel}\` == "${escapedValue}"`;
        }
      });
      dql += `, filter: {${filterExprs.join(' and ')}}`;
    }
    return dql;
  } else if (logProvider === 'newrelic' && providerType === 'metrics') {
    // NRQL output. Supported operators mirror NewRelicMetricSource.GetSupportedOperators
    // in api-server/services/observability/newrelic_metrics.go.
    const escapeValue = (v) => String(v).replace(/\\/g, '\\\\').replace(/'/g, "\\'");
    const escapeIdent = (s) => String(s).replace(/`/g, '');
    const ident = (s) => `\`${escapeIdent(s)}\``;

    const whereExprs = chips
      .filter((c) => c.label && c.value !== undefined && c.value !== '')
      .map((chip) => {
        const fld = ident(chip.label);
        const val = escapeValue(chip.value);
        const splitList = () =>
          String(chip.value || '')
            .split(',')
            .map((v) => v.trim())
            .filter(Boolean)
            .map((v) => `'${escapeValue(v)}'`)
            .join(', ');
        switch (chip.operator) {
          case '_eq':
          case '=':
            return `${fld} = '${val}'`;
          case '_neq':
          case '!=':
            return `${fld} != '${val}'`;
          case '_like':
          case '_ilike':
            return `${fld} LIKE '${val}'`;
          case '_contains':
            return `${fld} LIKE '%${val}%'`;
          case '_in':
            return `${fld} IN (${splitList()})`;
          case '_not_in':
            return `${fld} NOT IN (${splitList()})`;
          default:
            return `${fld} = '${val}'`;
        }
      });

    const baseMetric = ident((metricName || '').trim());
    const aggOp = operations?.map((o) => o.op?.toLowerCase())?.find((op) => ['sum', 'avg', 'average', 'min', 'max', 'count', 'rate'].includes(op));
    let selectExpr;
    switch (aggOp) {
      case 'sum':
        selectExpr = `sum(${baseMetric})`;
        break;
      case 'min':
        selectExpr = `min(${baseMetric})`;
        break;
      case 'max':
        selectExpr = `max(${baseMetric})`;
        break;
      case 'count':
        selectExpr = `count(${baseMetric})`;
        break;
      case 'rate':
        selectExpr = `rate(sum(${baseMetric}), 1 minute)`;
        break;
      case 'avg':
      case 'average':
      default:
        selectExpr = `average(${baseMetric})`;
    }

    let nrql = `SELECT ${selectExpr} FROM Metric`;
    if (whereExprs.length > 0) {
      nrql += ` WHERE ${whereExprs.join(' AND ')}`;
    }
    return nrql;
  } else if (
    logProvider == 'prometheus' ||
    logProvider == 'chronosphere' ||
    logProvider == 'victoria-metrics' ||
    (logProvider === 'newrelic' && providerType === 'traces')
  ) {
    const promFilters = chips.map((chip) => {
      const value = chip.value;
      switch (chip.operator) {
        case '=':
        case '_eq':
          return `${chip.label}="${value}"`;
        case '!=':
        case '_neq':
          return `${chip.label}!="${value}"`;
        case '=~':
        case '_regex':
          return `${chip.label}=~"${value}"`;
        case '!~':
        case '_nregex':
          return `${chip.label}!~"${value}"`;
        default:
          return `${chip.label}="${value}"`;
      }
    });
    const baseMetric = metricName?.trim() || 'up';
    let baseQuery = `${baseMetric}{${promFilters.join(',')}}`;
    operations
      ?.filter((o) => ['sum', 'avg', 'max', 'min', 'count'].includes(o.op.toLowerCase()) || o.value?.trim())
      .forEach((o) => {
        const rawVal = o.value.trim();
        switch (o.op.toLowerCase()) {
          case 'rate':
            baseQuery = `rate(${baseQuery}[${rawVal}])`;
            break;
          case 'irate':
            baseQuery = `irate(${baseQuery}[${rawVal}])`;
            break;
          case 'sum':
            baseQuery = `sum(${baseQuery})`;
            break;
          case 'avg':
            baseQuery = `avg(${baseQuery})`;
            break;
          case 'max':
            baseQuery = `max(${baseQuery})`;
            break;
          case 'min':
            baseQuery = `min(${baseQuery})`;
            break;
          case 'count':
            baseQuery = `count(${baseQuery})`;
            break;
          case 'topk':
            baseQuery = `topk(${rawVal}, ${baseQuery})`;
            break;
          case 'bottomk':
            baseQuery = `bottomk(${rawVal}, ${baseQuery})`;
            break;
          default:
            baseQuery = `${baseQuery} ${o.op} ${rawVal}`;
        }
      });
    return baseQuery;
  } else if (
    logProvider === 'loggly' ||
    logProvider == 'observe' ||
    logProvider == 'loki' ||
    logProvider == 'azure_app_insights' ||
    (logProvider === 'signoz' && providerType == 'logs') ||
    (logProvider === 'newrelic' && providerType == 'logs') ||
    (logProvider === 'dynatrace' && providerType == 'logs') ||
    logProvider === 'ES' ||
    (logProvider === 'solarwinds' && providerType == 'logs')
  ) {
    const operatorMap = {
      // Legacy UI values (kept for persisted state + providers still emitting them)
      '=': '_eq',
      '!=': '_neq',
      '<': '_lt',
      '<=': '_lte',
      '>': '_gt',
      '>=': '_gte',
      '=~': '_regex',
      '!~': '_nregex',
      CONTAINS: '_contains',
      'NOT CONTAINS': '_nlike',
      ICONTAINS: '_icontains',
      'NOT ICONTAINS': '_nlike',
      LIKE: '_like',
      ILIKE: '_ilike',
      'NOT LIKE': '_nlike',
      'NOT ILIKE': '_nlike',
      REGEX: '_regex',
      'NOT REGEX': '_nregex',
      REGEXP: '_regex',
      'NOT REGEXP': '_nregex',
      IN: '_in',
      'NOT IN': '_not_in',
      EXISTS: '_has_key',
      'NOT EXISTS': '_is_null',
      BETWEEN: '_between',
      // Backend-token identity (catalog-driven chips already emit these)
      _eq: '_eq',
      _neq: '_neq',
      _lt: '_lt',
      _lte: '_lte',
      _gt: '_gt',
      _gte: '_gte',
      _in: '_in',
      _not_in: '_not_in',
      _like: '_like',
      _ilike: '_ilike',
      _nlike: '_nlike',
      _contains: '_contains',
      _icontains: '_icontains',
      _nicontains: '_nlike',
      _regex: '_regex',
      _nregex: '_nregex',
      _has_key: '_has_key',
      _is_null: '_is_null',
      _between: '_between',
    };
    const createWhereClause = (filter) => {
      const { label, operator, value } = filter;
      const apiOperator = operatorMap[operator];
      if (!apiOperator) {
        console.warn(`Unsupported operator: ${operator}`);
        return null; // or throw new Error(...)
      }
      return {
        _binary: {
          [label]: {
            [apiOperator]: value,
          },
        },
      };
    };
    const whereClauses = chips.map(createWhereClause).filter(Boolean);

    // Unified map: covers both human-readable style (Dynatrace/Loki) and
    // API-prefixed style (NewRelic). All normalise to the same backend operators.
    const lineOperatorMap = {
      // Human-readable style (Dynatrace / Loki op.op values)
      CONTAINS: '_contains',
      'NOT CONTAINS': '_nlike',
      ICONTAINS: '_icontains',
      'NOT ICONTAINS': '_nlike',
      LIKE: '_like',
      ILIKE: '_ilike',
      'NOT LIKE': '_nlike',
      REGEX: '_regex',
      'NOT REGEX': '_nregex',
      // API-prefixed style (NewRelic op.op values, catalog-driven line filters)
      _contains: '_contains',
      _icontains: '_icontains',
      _nicontains: '_nlike',
      _like: '_like',
      _ilike: '_ilike',
      _nlike: '_nlike',
      _regex: '_regex',
      _nregex: '_nregex',
    };
    if (operations && operations.length > 0) {
      operations
        .filter((op) => op.value && op.value.trim() !== '')
        .forEach((op) => {
          const apiOperator = lineOperatorMap[op.op];
          if (apiOperator) {
            whereClauses.push({
              _binary: {
                content: {
                  [apiOperator]: op.value,
                },
              },
            });
          } else {
            console.warn(`Unsupported line operation operator: ${op.op}`);
          }
        });
    }

    return JSON.stringify(whereClauses);
  } else if (logProvider === 'datadog') {
    const filters = chips.map((chip) => {
      const { label, operator, value } = chip;
      switch (operator) {
        case '=':
          return `${label}:${value}`;
        default:
          return `${label}:${value}`;
      }
    });

    const filterString = filters.length ? `{${filters.join(',')}}` : '';
    const byClause = aggregatorBy?.length ? ` by {${aggregatorBy.join(',')}}` : '';

    return `${aggregator}:${metricName}${filterString}${byClause}`;
  }

  return '';
};
