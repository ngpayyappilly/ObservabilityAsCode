// Package dynatrace provides a typed client for the Dynatrace REST API.
// All methods are idempotent — callers don't need to distinguish create vs update.
package dynatrace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is a Dynatrace REST API client scoped to one tenant.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewClient creates a new Dynatrace API client.
func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// TenantURL returns the configured Dynatrace tenant base URL.
// Used by the dashboard controller to build direct UI links.
func (c *Client) TenantURL() string {
	return c.baseURL
}

// ── Internal HTTP helpers ─────────────────────────────────────────────────

func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Authorization", "Api-Token "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request %s %s: %w", method, path, err)
	}
	return resp, nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, body, out any) (int, error) {
	resp, err := c.do(ctx, method, path, body)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		return resp.StatusCode, fmt.Errorf("dynatrace API error %d: %s", resp.StatusCode, string(respBody))
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return resp.StatusCode, fmt.Errorf("unmarshal response: %w", err)
		}
	}
	return resp.StatusCode, nil
}

// ── SLO v2 API ────────────────────────────────────────────────────────────

// SLOPayload is the Dynatrace SLO v2 API request/response body.
type SLOPayload struct {
	ID                 string  `json:"id,omitempty"`
	Name               string  `json:"name"`
	Description        string  `json:"description,omitempty"`
	Enabled            bool    `json:"enabled"`
	EvaluationType     string  `json:"evaluationType"`
	Filter             string  `json:"filter"`
	Target             float64 `json:"target"`
	Warning            float64 `json:"warning"`
	Timeframe          string  `json:"timeframe"`
	UseRateMetric      bool    `json:"useRateMetric"`
	MetricNumerator    string  `json:"metricNumerator,omitempty"`
	MetricDenominator  string  `json:"metricDenominator,omitempty"`
	MetricExpression   string  `json:"metricExpression,omitempty"`
	ErrorBudgetBurnRate *SLOBurnRate `json:"errorBudgetBurnRate,omitempty"`
}

// SLOBurnRate configures burn rate visualization on the SLO.
type SLOBurnRate struct {
	FastBurnThreshold          int  `json:"fastBurnThreshold"`
	BurnRateVisualizationEnabled bool `json:"burnRateVisualizationEnabled"`
}

// SLOResponse is the response body from GET /api/v2/slos/{id}.
type SLOResponse struct {
	SLOPayload
	Status       string   `json:"status"`
	Error        string   `json:"error,omitempty"`
	EvaluatedPercentage float64 `json:"evaluatedPercentage,omitempty"`
	ErrorBudgetRemaining float64 `json:"errorBudgetRemaining,omitempty"`
}

// ApplySLO creates or updates an SLO. Returns the DT-assigned ID.
func (c *Client) ApplySLO(ctx context.Context, id string, payload SLOPayload) (string, error) {
	if id != "" {
		// Update existing SLO
		_, err := c.doJSON(ctx, http.MethodPut, "/api/v2/slos/"+id, payload, nil)
		if err != nil {
			return "", fmt.Errorf("update SLO %s: %w", id, err)
		}
		return id, nil
	}

	// Create new SLO
	var resp SLOResponse
	_, err := c.doJSON(ctx, http.MethodPost, "/api/v2/slos", payload, &resp)
	if err != nil {
		return "", fmt.Errorf("create SLO: %w", err)
	}
	return resp.ID, nil
}

// GetSLO fetches a specific SLO by ID.
func (c *Client) GetSLO(ctx context.Context, id string) (*SLOResponse, error) {
	var resp SLOResponse
	status, err := c.doJSON(ctx, http.MethodGet, "/api/v2/slos/"+id, nil, &resp)
	if status == http.StatusNotFound {
		return nil, nil // not found — caller should re-create
	}
	if err != nil {
		return nil, fmt.Errorf("get SLO %s: %w", id, err)
	}
	return &resp, nil
}

// DeleteSLO removes an SLO from Dynatrace.
func (c *Client) DeleteSLO(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/api/v2/slos/"+id, nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil // already gone
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("delete SLO %s: HTTP %d", id, resp.StatusCode)
	}
	return nil
}

// ── Metric Events (Alerts) API ────────────────────────────────────────────

// MetricEventPayload is the DT anomaly detection metric event API body.
type MetricEventPayload struct {
	ID          string              `json:"id,omitempty"`
	Name        string              `json:"metricEventName"`
	Description string              `json:"description,omitempty"`
	Enabled     bool                `json:"enabled"`
	Type        string              `json:"type"`
	QueryDef    MetricEventQueryDef `json:"queryDefinition"`
	ModelProps  MetricEventModel    `json:"modelProperties"`
	EventTpl    MetricEventTemplate `json:"eventTemplate"`
	AlertScope  []AlertScope        `json:"alertingScope,omitempty"`
}

// MetricEventQueryDef specifies which metric to evaluate.
type MetricEventQueryDef struct {
	Type        string `json:"type"`
	MetricKey   string `json:"metricKey"`
	Aggregation string `json:"aggregation"`
}

// MetricEventModel configures the threshold model.
type MetricEventModel struct {
	Type               string  `json:"type"`
	Threshold          float64 `json:"threshold"`
	AlertCondition     string  `json:"alertCondition"` // ABOVE | BELOW
	ViolatingSamples   int     `json:"violatingSamples"`
	Samples            int     `json:"samples"`
	DealertingSamples  int     `json:"dealertingSamples"`
}

// MetricEventTemplate defines the event payload fired on alert.
type MetricEventTemplate struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	EventType   string `json:"eventType"` // ERROR_EVENT | SLOWDOWN_EVENT | CUSTOM_ALERT
	DavisMerge bool   `json:"davisMerge"`
}

// AlertScope scopes the alert to a management zone.
type AlertScope struct {
	FilterType     string             `json:"filterType"`
	ManagementZone *MZRef             `json:"managementZone,omitempty"`
}

// MZRef references a management zone by ID.
type MZRef struct {
	ID string `json:"id"`
}

// MetricEventResponse is the DT API response body.
type MetricEventResponse struct {
	MetricEventPayload
}

// ApplyMetricEvent creates or updates a metric event alert.
func (c *Client) ApplyMetricEvent(ctx context.Context, id string, payload MetricEventPayload) (string, error) {
	const basePath = "/api/config/v1/anomalyDetection/metricEvents"
	if id != "" {
		_, err := c.doJSON(ctx, http.MethodPut, basePath+"/"+id, payload, nil)
		if err != nil {
			return "", fmt.Errorf("update metric event %s: %w", id, err)
		}
		return id, nil
	}

	var resp MetricEventResponse
	_, err := c.doJSON(ctx, http.MethodPost, basePath, payload, &resp)
	if err != nil {
		return "", fmt.Errorf("create metric event: %w", err)
	}
	return resp.ID, nil
}

// DeleteMetricEvent removes a metric event from Dynatrace.
func (c *Client) DeleteMetricEvent(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/api/config/v1/anomalyDetection/metricEvents/"+id, nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("delete metric event %s: HTTP %d", id, resp.StatusCode)
	}
	return nil
}

// ── Dashboard API ─────────────────────────────────────────────────────────

// DashboardPayload is the DT dashboard API body.
type DashboardPayload struct {
	ID       string          `json:"id,omitempty"`
	Metadata DashboardMeta   `json:"dashboardMetadata"`
	Tiles    []DashboardTile `json:"tiles"`
}

// DashboardMeta is dashboard display metadata.
type DashboardMeta struct {
	Name                     string   `json:"name"`
	Shared                   bool     `json:"shared"`
	Owner                    string   `json:"owner"`
	Tags                     []string `json:"tags,omitempty"`
	DashboardFilter          *DashboardFilter `json:"dashboardFilter,omitempty"`
}

// DashboardFilter scopes the dashboard to a management zone.
type DashboardFilter struct {
	ManagementZone *MZFilterRef `json:"managementZone,omitempty"`
}

// MZFilterRef references a management zone in a dashboard filter.
type MZFilterRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// DashboardTile is one panel in the dashboard grid.
// The DT API uses a 304px grid unit; widths/heights are multiples of 304.
type DashboardTile struct {
	// Common fields
	Name       string     `json:"name"`
	TileType   string     `json:"tileType"` // SLO | DATA_EXPLORER | HEADER | MARKDOWN
	Configured bool       `json:"configured"`
	Bounds     TileBounds `json:"bounds"`

	// SLO tile fields
	SLOID                    string `json:"sloId,omitempty"`
	Metric                   string `json:"metric,omitempty"`                   // FUNC:slo.target | FUNC:slo.status
	ExcludeMaintenanceWindows bool   `json:"excludeMaintenanceWindows,omitempty"`

	// DATA_EXPLORER tile fields
	Queries      []TileQuery   `json:"queries,omitempty"`
	VisualConfig *VisualConfig `json:"visualConfig,omitempty"`

	// MARKDOWN tile
	Markdown string `json:"markdown,omitempty"`
}

// TileBounds positions a tile in the dashboard grid (units of 304px).
type TileBounds struct {
	Top    int `json:"top"`
	Left   int `json:"left"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// TileQuery is a metric selector query inside a DATA_EXPLORER tile.
type TileQuery struct {
	ID               string `json:"id"`
	MetricSelector   string `json:"metricSelector"`
	SpaceAggregation string `json:"spaceAggregation,omitempty"`
	TimeAggregation  string `json:"timeAggregation,omitempty"`
	Enabled          bool   `json:"enabled"`
}

// VisualConfig controls how a DATA_EXPLORER tile renders.
type VisualConfig struct {
	Type   string        `json:"type"` // GRAPH_CHART | SINGLE_VALUE | PIE_CHART | TABLE
	Global *GlobalConfig `json:"global,omitempty"`
	Thresholds []ThresholdConfig `json:"thresholds,omitempty"`
}

// GlobalConfig controls axis and legend display.
type GlobalConfig struct {
	HideLegend bool `json:"hideLegend,omitempty"`
}

// ThresholdConfig defines color bands on a chart.
type ThresholdConfig struct {
	AxisTarget string            `json:"axisTarget"`
	Rules      []ThresholdRule   `json:"rules,omitempty"`
}

// ThresholdRule sets a color above a value threshold.
type ThresholdRule struct {
	Color     string  `json:"color"`
	Condition string  `json:"condition"`
	Value     float64 `json:"value"`
}

// ApplyDashboard creates or updates a dashboard.
func (c *Client) ApplyDashboard(ctx context.Context, id string, payload DashboardPayload) (string, error) {
	const basePath = "/api/config/v1/dashboards"
	if id != "" {
		payload.ID = id
		_, err := c.doJSON(ctx, http.MethodPut, basePath+"/"+id, payload, nil)
		if err != nil {
			return "", fmt.Errorf("update dashboard %s: %w", id, err)
		}
		return id, nil
	}

	var resp DashboardPayload
	_, err := c.doJSON(ctx, http.MethodPost, basePath, payload, &resp)
	if err != nil {
		return "", fmt.Errorf("create dashboard: %w", err)
	}
	return resp.ID, nil
}

// DeleteDashboard removes a dashboard from Dynatrace.
func (c *Client) DeleteDashboard(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/api/config/v1/dashboards/"+id, nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("delete dashboard %s: HTTP %d", id, resp.StatusCode)
	}
	return nil
}

// ── Entity resolution ─────────────────────────────────────────────────────

// EntityResponse is the DT entity API response.
type EntityResponse struct {
	EntityID    string `json:"entityId"`
	DisplayName string `json:"displayName"`
}

// EntityListResponse wraps a list of entities.
type EntityListResponse struct {
	Entities []EntityResponse `json:"entities"`
}

// ResolveServiceEntity finds a SERVICE entity by tag (backstage-id:<name>).
func (c *Client) ResolveServiceEntity(ctx context.Context, backstageID string) (string, error) {
	path := fmt.Sprintf(
		"/api/v2/entities?entitySelector=type(\"SERVICE\"),tag(\"backstage-id:%s\")&fields=entityId,displayName",
		backstageID,
	)
	var resp EntityListResponse
	_, err := c.doJSON(ctx, http.MethodGet, path, nil, &resp)
	if err != nil {
		return "", fmt.Errorf("resolve service entity for %s: %w", backstageID, err)
	}
	if len(resp.Entities) == 0 {
		return "", fmt.Errorf("no SERVICE entity found with tag backstage-id:%s — ensure the pod has the label and auto-tag rule has run", backstageID)
	}
	return resp.Entities[0].EntityID, nil
}
