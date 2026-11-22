package dashboard

import (
	"encoding/json"
	"fmt"
	"strings"
)

/*
Chart data links — a timeseries panel's drill-downs. Clicking a point opens a
menu of these links, each an in-app path templated from the clicked series'
labels, its account and the chart's time range.

The renderer's counterpart is app/src/components/k8s/dashboards/panelColumns.ts
(renderDataLinks). Both ends validate the URL for the same reason a table link
is validated twice: this refuses to STORE a definition that would render a
dangerous link, and the frontend refuses to render one, because a definition can
also arrive through dashboard import. The URL rule itself is validLinkUrl, shared
with the table columns.
*/

// DataLink is one drill-down offered on a point click: a menu title and an in-app
// path with `{{token}}` placeholders the renderer fills from the clicked point.
type DataLink struct {
	Title string `json:"title"`
	Url   string `json:"url"`
}

// chartOptions is the subset of a panel's `options` this file understands.
// Anything else in there is carried through untouched.
type chartOptions struct {
	DataLinks []DataLink `json:"data_links,omitempty"`
}

// decodeChartOptions reads the data_links out of a panel's options, round-tripped
// through JSON so the `json` tags govern — the same way the table options decode.
func decodeChartOptions(options map[string]any) (chartOptions, error) {
	var opts chartOptions
	if len(options) == 0 {
		return opts, nil
	}
	body, err := json.Marshal(options)
	if err != nil {
		return opts, fmt.Errorf("options are not readable: %w", err)
	}
	if err := json.Unmarshal(body, &opts); err != nil {
		return opts, fmt.Errorf("options are not readable: %w", err)
	}
	return opts, nil
}

// validateDataLinks rejects chart drill-down links the renderer could not open,
// at save rather than on every viewer's screen.
func validateDataLinks(p Panel) error {
	opts, err := decodeChartOptions(p.Options)
	if err != nil {
		return fmt.Errorf("panel %q: %w", p.Title, err)
	}
	if len(opts.DataLinks) == 0 {
		return nil
	}
	// Only a metrics timeseries panel has points to click. Every other datasource
	// renders a table (logs, nudgebee, commands) or, for traces, returns table
	// data with no series even when drawn as a chart — so a data link there is
	// config nothing can fire. Symmetric to the table-column rule above.
	if p.Type != VizTimeseries || p.Datasource != DatasourceMetrics {
		return fmt.Errorf("panel %q: data links only apply to a metrics timeseries panel", p.Title)
	}
	for _, l := range opts.DataLinks {
		title := strings.TrimSpace(l.Title)
		if title == "" {
			return fmt.Errorf("panel %q: a data link needs a title", p.Title)
		}
		// Every viewer of this dashboard follows these links, so an external or
		// scripted destination is refused rather than stored. See validLinkUrl.
		if !validLinkUrl(strings.TrimSpace(l.Url)) {
			return fmt.Errorf("panel %q: the data link %q must point at a path inside the product, e.g. /kubernetes/details/{{__account_id}}", p.Title, title)
		}
	}
	return nil
}
