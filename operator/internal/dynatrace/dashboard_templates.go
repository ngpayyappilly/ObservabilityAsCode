// Package dynatrace — dashboard template engine.
//
// Three templates are defined:
//
//	service-overview   Standard 4-row layout: SLO health, request/error rates,
//	                   latency percentiles, error budget burn rate.
//
//	slo-report         SLO-focused layout: availability + latency status, error
//	                   budget remaining, burn rate trends (1h and 6h windows).
//
//	endpoint-detail    Per-endpoint breakdown: top requests by throughput, error
//	                   rate per endpoint, latency p50/p95/p99 heatmap.
//
// Grid units: the DT dashboard grid is 304px wide per column.
// Standard widths:
//   - 304  = 1 column (SLO tiles)
//   - 608  = 2 columns (small chart)
//   - 912  = 3 columns (medium chart)
//   - 1216 = 4 columns (full-width)
// Row heights: 304 (standard) or 152 (header/markdown strip).

package dynatrace

import "fmt"

// TemplateData holds the values substituted into each dashboard template.
type TemplateData struct {
	ServiceName    string   // e.g. "payments-api"
	Environment    string   // e.g. "prod"
	ManagementZone string   // e.g. "env:prod"
	SLOIDs         []string // resolved DT SLO IDs, in the order of spec.sloRefs
	SLONames       []string // human-readable label for each SLO ID
}

// BuildDashboard returns the full DT dashboard payload for the given template name.
func BuildDashboard(template string, d TemplateData) (DashboardPayload, error) {
	switch template {
	case "service-overview", "":
		return serviceOverviewTemplate(d), nil
	case "slo-report":
		return sloReportTemplate(d), nil
	case "endpoint-detail":
		return endpointDetailTemplate(d), nil
	default:
		return DashboardPayload{}, fmt.Errorf("unknown dashboard template %q — valid values: service-overview, slo-report, endpoint-detail", template)
	}
}

// ── service-overview ──────────────────────────────────────────────────────
//
// Layout (each row = 304px tall, grid = 1216px wide):
//
//   Row 0: [Header strip — service name + environment]
//   Row 1: [SLO-1 304] [SLO-2 304] [Request Rate 608]
//   Row 2: [Error Rate 608] [p99 Latency 608]
//   Row 3: [Error Budget Burn (full-width 1216)]
//   Row 4: [p50 304] [p95 304] [p99 304] [Throughput 304]

func serviceOverviewTemplate(d TemplateData) DashboardPayload {
	tiles := []DashboardTile{}

	// ── Row 0: Header ────────────────────────────────────────────────────
	tiles = append(tiles, markdownTile(
		fmt.Sprintf("## %s — Service Overview (%s)\nManagement zone: `%s`", d.ServiceName, d.Environment, d.ManagementZone),
		bounds(0, 0, 1216, 152),
	))

	// ── Row 1: SLO tiles + request rate ─────────────────────────────────
	left := 0
	for i, sloID := range d.SLOIDs {
		label := fmt.Sprintf("SLO %d", i+1)
		if i < len(d.SLONames) {
			label = d.SLONames[i]
		}
		tiles = append(tiles, sloTile(label, sloID, bounds(152, left, 304, 304)))
		left += 304
	}

	// Fill remaining row 1 width with request rate chart
	reqWidth := 1216 - left
	if reqWidth > 0 {
		tiles = append(tiles, dataExplorerTile(
			"Request Rate (req/min)",
			bounds(152, left, reqWidth, 304),
			[]TileQuery{{
				ID:               "A",
				MetricSelector:   mzFilter("builtin:service.requestCount.total", d.ManagementZone, "sum"),
				SpaceAggregation: "SUM",
				TimeAggregation:  "DEFAULT",
				Enabled:          true,
			}},
			graphChart(false),
		))
	}

	// ── Row 2: Error rate + p99 latency ──────────────────────────────────
	tiles = append(tiles, dataExplorerTile(
		"Error Rate (%)",
		bounds(456, 0, 608, 304),
		[]TileQuery{{
			ID:             "A",
			MetricSelector: mzFilter("builtin:service.errors.total.rate", d.ManagementZone, "avg"),
			Enabled:        true,
		}},
		graphChartWithThresholds(
			[]ThresholdRule{
				{Color: "#7dc540", Condition: "ABOVE", Value: 0},
				{Color: "#f5d30f", Condition: "ABOVE", Value: 0.5},
				{Color: "#dc172a", Condition: "ABOVE", Value: 1.0},
			},
		),
	))

	tiles = append(tiles, dataExplorerTile(
		"p99 Latency (ms)",
		bounds(456, 608, 608, 304),
		[]TileQuery{{
			ID:             "A",
			MetricSelector: mzFilter("builtin:service.response.time:percentile(99)", d.ManagementZone, "avg"),
			Enabled:        true,
		}},
		graphChart(false),
	))

	// ── Row 3: Error budget burn rate (full width) ─────────────────────
	tiles = append(tiles, dataExplorerTile(
		"Error Budget Burn Rate",
		bounds(760, 0, 1216, 304),
		buildBurnRateQueries(d.SLOIDs),
		graphChartWithThresholds(
			[]ThresholdRule{
				{Color: "#7dc540", Condition: "ABOVE", Value: 0},
				{Color: "#f5d30f", Condition: "ABOVE", Value: 6},
				{Color: "#dc172a", Condition: "ABOVE", Value: 14},
			},
		),
	))

	// ── Row 4: Latency percentile breakdown ───────────────────────────────
	for i, percentile := range []string{"50", "75", "95", "99"} {
		tiles = append(tiles, dataExplorerTile(
			fmt.Sprintf("p%s Latency (ms)", percentile),
			bounds(1064, i*304, 304, 304),
			[]TileQuery{{
				ID:             "A",
				MetricSelector: mzFilter(fmt.Sprintf("builtin:service.response.time:percentile(%s)", percentile), d.ManagementZone, "avg"),
				Enabled:        true,
			}},
			singleValueChart(),
		))
	}

	return DashboardPayload{
		Metadata: dashboardMeta(
			fmt.Sprintf("%s — Service Overview (%s)", d.ServiceName, d.Environment),
			d.ServiceName, d.Environment, d.ManagementZone,
		),
		Tiles: tiles,
	}
}

// ── slo-report ────────────────────────────────────────────────────────────
//
// Layout:
//   Row 0: [Header]
//   Row 1: [SLO-1 304] [SLO-2 304] [SLO Status History 608]
//   Row 2: [Error Budget Remaining 608] [Fast Burn 1h 608]
//   Row 3: [Slow Burn 6h 608] [SLO Target vs Actual 608]

func sloReportTemplate(d TemplateData) DashboardPayload {
	tiles := []DashboardTile{}

	// Row 0: header
	tiles = append(tiles, markdownTile(
		fmt.Sprintf("## %s — SLO Report (%s)\nError budget and burn rate analysis. Zone: `%s`", d.ServiceName, d.Environment, d.ManagementZone),
		bounds(0, 0, 1216, 152),
	))

	// Row 1: SLO status tiles + history
	left := 0
	for i, sloID := range d.SLOIDs {
		label := sloLabel(d, i)
		tiles = append(tiles, sloTile(label, sloID, bounds(152, left, 304, 304)))
		left += 304
	}
	histWidth := 1216 - left
	if histWidth > 0 && len(d.SLOIDs) > 0 {
		tiles = append(tiles, dataExplorerTile(
			"SLO Compliance History",
			bounds(152, left, histWidth, 304),
			buildSLOComplianceQueries(d.SLOIDs),
			graphChart(false),
		))
	}

	// Row 2: error budget remaining + fast burn
	tiles = append(tiles, dataExplorerTile(
		"Error Budget Remaining (min)",
		bounds(456, 0, 608, 304),
		buildErrorBudgetQueries(d.SLOIDs),
		graphChart(false),
	))
	tiles = append(tiles, dataExplorerTile(
		"Fast Burn Rate (1h window)",
		bounds(456, 608, 608, 304),
		buildBurnRateWindowQueries(d.SLOIDs, "1h"),
		graphChartWithThresholds([]ThresholdRule{
			{Color: "#7dc540", Condition: "ABOVE", Value: 0},
			{Color: "#dc172a", Condition: "ABOVE", Value: 14},
		}),
	))

	// Row 3: slow burn + target vs actual
	tiles = append(tiles, dataExplorerTile(
		"Slow Burn Rate (6h window)",
		bounds(760, 0, 608, 304),
		buildBurnRateWindowQueries(d.SLOIDs, "6h"),
		graphChartWithThresholds([]ThresholdRule{
			{Color: "#7dc540", Condition: "ABOVE", Value: 0},
			{Color: "#f5d30f", Condition: "ABOVE", Value: 6},
		}),
	))
	tiles = append(tiles, dataExplorerTile(
		"Availability vs SLO Target",
		bounds(760, 608, 608, 304),
		[]TileQuery{{
			ID:             "A",
			MetricSelector: mzFilter("builtin:service.errors.total.successCount", d.ManagementZone, "sum"),
			Enabled:        true,
		}},
		graphChart(false),
	))

	return DashboardPayload{
		Metadata: dashboardMeta(
			fmt.Sprintf("%s — SLO Report (%s)", d.ServiceName, d.Environment),
			d.ServiceName, d.Environment, d.ManagementZone,
		),
		Tiles: tiles,
	}
}

// ── endpoint-detail ───────────────────────────────────────────────────────
//
// Layout:
//   Row 0: [Header]
//   Row 1: [Top Endpoints by Throughput 608] [Top Endpoints by Error Rate 608]
//   Row 2: [p50 by Endpoint 608] [p99 by Endpoint 608]
//   Row 3: [Slowest Endpoints Table 1216]

func endpointDetailTemplate(d TemplateData) DashboardPayload {
	tiles := []DashboardTile{}

	tiles = append(tiles, markdownTile(
		fmt.Sprintf("## %s — Endpoint Detail (%s)\nPer-endpoint throughput, error rate, and latency breakdown.", d.ServiceName, d.Environment),
		bounds(0, 0, 1216, 152),
	))

	// Row 1: throughput + error rate split by SERVICE_METHOD entity
	tiles = append(tiles, dataExplorerTile(
		"Throughput by Endpoint (req/min)",
		bounds(152, 0, 608, 304),
		[]TileQuery{{
			ID:               "A",
			MetricSelector:   fmt.Sprintf("builtin:service.keyRequest.count.total:filter(tag(\"service:%s\")):splitBy(\"dt.entity.service_method\"):sum:sort(value(sum,descending)):limit(10)", d.ServiceName),
			SpaceAggregation: "SUM",
			Enabled:          true,
		}},
		graphChart(false),
	))
	tiles = append(tiles, dataExplorerTile(
		"Error Rate by Endpoint (%)",
		bounds(152, 608, 608, 304),
		[]TileQuery{{
			ID:             "A",
			MetricSelector: fmt.Sprintf("builtin:service.keyRequest.errorCount:filter(tag(\"service:%s\")):splitBy(\"dt.entity.service_method\"):sum:sort(value(sum,descending)):limit(10)", d.ServiceName),
			Enabled:        true,
		}},
		graphChartWithThresholds([]ThresholdRule{
			{Color: "#7dc540", Condition: "ABOVE", Value: 0},
			{Color: "#dc172a", Condition: "ABOVE", Value: 1.0},
		}),
	))

	// Row 2: latency percentiles per endpoint
	tiles = append(tiles, dataExplorerTile(
		"p50 Latency by Endpoint (ms)",
		bounds(456, 0, 608, 304),
		[]TileQuery{{
			ID:             "A",
			MetricSelector: fmt.Sprintf("builtin:service.keyRequest.response.time:percentile(50):filter(tag(\"service:%s\")):splitBy(\"dt.entity.service_method\"):avg:sort(value(avg,descending)):limit(10)", d.ServiceName),
			Enabled:        true,
		}},
		graphChart(false),
	))
	tiles = append(tiles, dataExplorerTile(
		"p99 Latency by Endpoint (ms)",
		bounds(456, 608, 608, 304),
		[]TileQuery{{
			ID:             "A",
			MetricSelector: fmt.Sprintf("builtin:service.keyRequest.response.time:percentile(99):filter(tag(\"service:%s\")):splitBy(\"dt.entity.service_method\"):avg:sort(value(avg,descending)):limit(10)", d.ServiceName),
			Enabled:        true,
		}},
		graphChart(false),
	))

	// Row 3: full-width slowest endpoints table
	tiles = append(tiles, dataExplorerTile(
		"Slowest Endpoints (p99)",
		bounds(760, 0, 1216, 304),
		[]TileQuery{{
			ID:             "A",
			MetricSelector: fmt.Sprintf("builtin:service.keyRequest.response.time:percentile(99):filter(tag(\"service:%s\")):splitBy(\"dt.entity.service_method\"):avg:sort(value(avg,descending)):limit(20)", d.ServiceName),
			Enabled:        true,
		}},
		tableChart(),
	))

	return DashboardPayload{
		Metadata: dashboardMeta(
			fmt.Sprintf("%s — Endpoint Detail (%s)", d.ServiceName, d.Environment),
			d.ServiceName, d.Environment, d.ManagementZone,
		),
		Tiles: tiles,
	}
}

// ── Tile constructors ─────────────────────────────────────────────────────

func sloTile(name, sloID string, b TileBounds) DashboardTile {
	return DashboardTile{
		Name:                     name,
		TileType:                 "SLO",
		Configured:               true,
		Bounds:                   b,
		SLOID:                    sloID,
		Metric:                   "FUNC:slo.target",
		ExcludeMaintenanceWindows: true,
	}
}

func dataExplorerTile(name string, b TileBounds, queries []TileQuery, vc *VisualConfig) DashboardTile {
	return DashboardTile{
		Name:         name,
		TileType:     "DATA_EXPLORER",
		Configured:   true,
		Bounds:       b,
		Queries:      queries,
		VisualConfig: vc,
	}
}

func markdownTile(content string, b TileBounds) DashboardTile {
	return DashboardTile{
		Name:       "",
		TileType:   "MARKDOWN",
		Configured: true,
		Bounds:     b,
		Markdown:   content,
	}
}

// ── Visual config helpers ─────────────────────────────────────────────────

func graphChart(hideLegend bool) *VisualConfig {
	return &VisualConfig{
		Type:   "GRAPH_CHART",
		Global: &GlobalConfig{HideLegend: hideLegend},
	}
}

func graphChartWithThresholds(rules []ThresholdRule) *VisualConfig {
	return &VisualConfig{
		Type:   "GRAPH_CHART",
		Global: &GlobalConfig{},
		Thresholds: []ThresholdConfig{
			{AxisTarget: "LEFT", Rules: rules},
		},
	}
}

func singleValueChart() *VisualConfig {
	return &VisualConfig{Type: "SINGLE_VALUE"}
}

func tableChart() *VisualConfig {
	return &VisualConfig{Type: "TABLE"}
}

// ── Metric selector helpers ───────────────────────────────────────────────

// mzFilter wraps a metric key with a management zone filter and aggregation.
func mzFilter(metricKey, managementZone, aggregation string) string {
	return fmt.Sprintf(`%s:filter(mzName("%s")):%s:auto`, metricKey, managementZone, aggregation)
}

// buildBurnRateQueries creates one query per SLO ID for the burn rate tile.
func buildBurnRateQueries(sloIDs []string) []TileQuery {
	queries := make([]TileQuery, 0, len(sloIDs))
	for i, id := range sloIDs {
		queries = append(queries, TileQuery{
			ID:             string(rune('A' + i)),
			MetricSelector: fmt.Sprintf(`ext:slo.errorBudgetBurnRate:filter(eq(sloId,"%s")):avg`, id),
			Enabled:        true,
		})
	}
	return queries
}

// buildBurnRateWindowQueries creates burn rate queries for a specific time window.
func buildBurnRateWindowQueries(sloIDs []string, window string) []TileQuery {
	queries := make([]TileQuery, 0, len(sloIDs))
	for i, id := range sloIDs {
		queries = append(queries, TileQuery{
			ID:             string(rune('A' + i)),
			MetricSelector: fmt.Sprintf(`ext:slo.errorBudgetBurnRate:filter(eq(sloId,"%s")):avg:resolution(%s)`, id, window),
			Enabled:        true,
		})
	}
	return queries
}

// buildErrorBudgetQueries creates error budget remaining queries per SLO.
func buildErrorBudgetQueries(sloIDs []string) []TileQuery {
	queries := make([]TileQuery, 0, len(sloIDs))
	for i, id := range sloIDs {
		queries = append(queries, TileQuery{
			ID:             string(rune('A' + i)),
			MetricSelector: fmt.Sprintf(`ext:slo.errorBudget:filter(eq(sloId,"%s")):avg`, id),
			Enabled:        true,
		})
	}
	return queries
}

// buildSLOComplianceQueries creates SLO percentage queries for the history chart.
func buildSLOComplianceQueries(sloIDs []string) []TileQuery {
	queries := make([]TileQuery, 0, len(sloIDs))
	for i, id := range sloIDs {
		queries = append(queries, TileQuery{
			ID:             string(rune('A' + i)),
			MetricSelector: fmt.Sprintf(`ext:slo.status:filter(eq(sloId,"%s")):avg`, id),
			Enabled:        true,
		})
	}
	return queries
}

// ── Shared helpers ────────────────────────────────────────────────────────

func bounds(top, left, width, height int) TileBounds {
	return TileBounds{Top: top, Left: left, Width: width, Height: height}
}

func sloLabel(d TemplateData, i int) string {
	if i < len(d.SLONames) && d.SLONames[i] != "" {
		return d.SLONames[i]
	}
	return fmt.Sprintf("SLO %d", i+1)
}

func dashboardMeta(name, serviceName, environment, mz string) DashboardMeta {
	return DashboardMeta{
		Name:  name,
		Owner: "sre-team",
		Tags:  []string{"oac", serviceName, environment},
		DashboardFilter: &DashboardFilter{
			ManagementZone: &MZFilterRef{Name: mz},
		},
	}
}
