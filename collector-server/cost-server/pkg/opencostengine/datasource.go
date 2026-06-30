package opencostengine

import (
	"github.com/opencost/opencost/core/pkg/source"
	"github.com/opencost/opencost/modules/prometheus-source/pkg/prom"
)

// nudgebeeDataSource wraps the upstream PrometheusDataSource and swaps in the
// kube-state-metrics querier via Metrics(). All other OpenCostDataSource methods
// (ClusterMap, ClusterInfo, BatchDuration, Resolution, Register*, …) delegate to
// the embedded upstream data source. This is the single seam that lets the cost
// model run server-side over standard metrics, with no fork of upstream OpenCost.
type nudgebeeDataSource struct {
	*prom.PrometheusDataSource
	querier source.MetricsQuerier
}

func newNudgebeeDataSource(pds *prom.PrometheusDataSource) *nudgebeeDataSource {
	return &nudgebeeDataSource{
		PrometheusDataSource: pds,
		querier:              newNudgebeeQuerier(pds),
	}
}

func (ds *nudgebeeDataSource) Metrics() source.MetricsQuerier { return ds.querier }
