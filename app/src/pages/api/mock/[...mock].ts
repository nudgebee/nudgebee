import type { NextApiRequest, NextApiResponse } from 'next';
import home from './json/home.json';
import k8sApplication from './json/k8s-application.json';
import k8sDashboard from './json/k8s-dashboard.json';
import k8sEventsGrouping from './json/k8s-events-grouping.json';
import k8sEvents from './json/k8s-events.json';
import k8sInvestigation from './json/k8s-investigation.json';
import k8sNamepace from './json/k8s-namespace.json';
import k8sNode from './json/k8s-node.json';
import k8sPodGroupings from './json/k8s-pod-groupings.json';
import k8sPod from './json/k8s-pod.json';
import k8sRelay from './json/k8s-relay.json';
import k8sTrace from './json/k8s-trace.json';
import eventRules from './json/event-rules.json';
import slo from './json/slo.json';
import recommendations from './json/recommendation.json';
import k8sOptimizeTabCount from './json/k8s-recommendation-count.json';
import k8sOptimizeAutoPilotCount from './json/k8s-optimize-summary-autopilot.json';
import k8sOptimizeInfographics from './json/k8s-optimize-summary-infographics.json';
import k8sEventTypesCount from './json/k8s-event-types-count.json';
import k8sIndividualAggregationKeyCount from './json/k8s-individual-aggregation-key-count.json';
import k8sTracesServiceOperation from './json/k8s-traces-service-operation.json';
import newHome from './json/new-home.json';
import k8sAnomaly from './json/k8s-anomaly.json';

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  if (req.method === 'GET') {
    let mockQuery = '';
    if (Array.isArray(req.query.mock)) {
      mockQuery = req.query.mock.join('/');
    } else {
      mockQuery = req.query.mock as string;
    }
    if (mockQuery === 'home') {
      return res.status(200).json(home);
    } else if (mockQuery === 'k8s-applications') {
      return res.status(200).json(k8sApplication);
    } else if (mockQuery === 'k8s-dashboard') {
      return res.status(200).json(k8sDashboard);
    } else if (mockQuery === 'k8s-events-grouping') {
      return res.status(200).json(k8sEventsGrouping);
    } else if (mockQuery === 'k8s-events') {
      return res.status(200).json(k8sEvents);
    } else if (mockQuery === 'k8s-investigation') {
      return res.status(200).json(k8sInvestigation);
    } else if (mockQuery === 'k8s-namespaces') {
      return res.status(200).json(k8sNamepace);
    } else if (mockQuery === 'k8s-nodes') {
      return res.status(200).json(k8sNode);
    } else if (mockQuery === 'k8s-pod-groupings') {
      return res.status(200).json(k8sPodGroupings);
    } else if (mockQuery === 'k8s-pods') {
      return res.status(200).json(k8sPod);
    } else if (mockQuery === 'k8s-relay') {
      return res.status(200).json(k8sRelay);
    } else if (mockQuery === 'k8s-traces') {
      return res.status(200).json(k8sTrace);
    } else if (mockQuery === 'event-rules') {
      return res.status(200).json(eventRules);
    } else if (mockQuery === 'slo') {
      return res.status(200).json(slo);
    } else if (mockQuery === 'recommendations') {
      return res.status(200).json(recommendations);
    } else if (mockQuery == 'recommendationTabCounts') {
      return res.status(200).json(k8sOptimizeTabCount);
    } else if (mockQuery == 'optimizeSummaryAutoPilotCounts') {
      return res.status(200).json(k8sOptimizeAutoPilotCount);
    } else if (mockQuery == 'optimizeSummaryInfographics') {
      return res.status(200).json(k8sOptimizeInfographics);
    } else if (mockQuery == 'eventTypesCount') {
      return res.status(200).json(k8sEventTypesCount);
    } else if (mockQuery == 'individualAggregationKeyCount') {
      return res.status(200).json(k8sIndividualAggregationKeyCount);
    } else if (mockQuery == 'k8s-traces-service-operation') {
      return res.status(200).json(k8sTracesServiceOperation);
    } else if (mockQuery === 'new-home') {
      return res.status(200).json(newHome);
    } else if (mockQuery === 'k8s-anomaly') {
      return res.status(200).json(k8sAnomaly);
    }
  }
  res.status(404).json({
    message: 'not found',
    query: req.query,
  });
}
