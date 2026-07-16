package management

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/usageledger"
)

func openManagementUsageStore(t *testing.T) *usageledger.SQLiteStore {
	t.Helper()
	store, err := usageledger.OpenSQLite(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("open usage store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func usageManagementTestRouter(h *Handler) *gin.Engine {
	router := gin.New()
	public := router.Group("/v0/public")
	public.GET("/usage-viewer", h.GetPublicUsageViewer)
	public.POST("/usage-analytics", h.PostPublicUsageAnalytics)
	group := router.Group("/v0/management")
	group.GET("/model-prices", h.GetModelPrices)
	group.PUT("/model-prices", h.PutModelPrices)
	group.PATCH("/model-prices/:model", h.PatchModelPrice)
	group.DELETE("/model-prices/:model", h.DeleteModelPrice)
	group.GET("/usage-summary", h.GetUsageSummary)
	group.POST("/usage-analytics", h.PostUsageAnalytics)
	return router
}

func performUsageManagementJSON(method, target string, body any, router http.Handler) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	return rec
}

func TestModelPricesPatchAndList(t *testing.T) {
	store := openManagementUsageStore(t)
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, nil)
	h.SetUsageLedger(store)
	router := usageManagementTestRouter(h)

	price := usageledger.ModelPrice{
		Model:              "gpt-5.5",
		InputPer1M:         10,
		OutputPer1M:        20,
		CacheReadPer1M:     1,
		CacheCreationPer1M: 5,
	}
	rec := performUsageManagementJSON(http.MethodPatch, "/v0/management/model-prices/gpt-5.5", price, router)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	rec = performUsageManagementJSON(http.MethodGet, "/v0/management/model-prices", nil, router)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Prices []usageledger.ModelPrice `json:"prices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	var found *usageledger.ModelPrice
	for i := range body.Prices {
		if body.Prices[i].Model == "gpt-5.5" {
			found = &body.Prices[i]
			break
		}
	}
	if found == nil || found.InputPer1M != 10 {
		t.Fatalf("prices = %#v", body.Prices)
	}
}
