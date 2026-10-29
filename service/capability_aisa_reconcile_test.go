package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

func TestFetchAISAUsageAndReconcileAggregation(t *testing.T) {
	truncate(t)
	start := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-aisa-key" {
			t.Errorf("missing bearer credential")
		}
		if r.URL.Query().Get("start_time") != "1793232000" ||
			r.URL.Query().Get("end_time") != "1793318400" ||
			r.URL.Query().Get("bucket_width") != "1d" ||
			r.URL.Query().Get("scope") != "key" {
			t.Errorf("unexpected usage query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"currency":"USD",
			"scope":"key",
			"buckets":[
				{"start_time":1793232000,"end_time":1793318400,"charged_micros_usd":123456},
				{"start_time":1793318400,"end_time":1793404800,"charged_micros_usd":999}
			]
		}`))
	}))
	defer server.Close()
	originalEndpoint := aisaUsageEndpoint
	aisaUsageEndpoint = server.URL
	t.Cleanup(func() { aisaUsageEndpoint = originalEndpoint })

	buckets, err := fetchAISAUsage(context.Background(), "test-aisa-key", start, end)
	if err != nil {
		t.Fatalf("fetch usage: %v", err)
	}
	if len(buckets) != 2 {
		t.Fatalf("unexpected buckets: %#v", buckets)
	}

	otherAISA := map[string]any{
		"provider":       "aisa",
		"usage_unit":     "micro_usd",
		"usage_quantity": float64(100000),
	}
	otherNonAISA := map[string]any{
		"provider":       "other",
		"usage_unit":     "micro_usd",
		"usage_quantity": float64(500000),
	}
	rows := []model.Log{
		{UserId: 1, CreatedAt: start.Unix() + 10, Type: model.LogTypeConsume, Quota: 999999, Other: common.MapToJsonStr(otherAISA)},
		{UserId: 1, CreatedAt: start.Unix() + 20, Type: model.LogTypeConsume, Quota: 999999, Other: common.MapToJsonStr(otherNonAISA)},
	}
	if err := model.LOG_DB.Create(&rows).Error; err != nil {
		t.Fatalf("seed logs: %v", err)
	}
	recorded, err := sumRecordedAISAUsage(start, end)
	if err != nil {
		t.Fatalf("sum recorded usage: %v", err)
	}
	result := reconcileAISAUsage("2026-10-29", buckets, recorded)
	if result.UpstreamMicrosUSD != 123456 || result.RecordedMicrosUSD != 100000 ||
		result.DifferenceMicrosUSD != -23456 || result.Alert {
		t.Fatalf("unexpected reconciliation: %#v", result)
	}
}

func TestAISAReconciliationThresholds(t *testing.T) {
	day := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC).Unix()
	tests := []struct {
		name      string
		upstream  int64
		recorded  int64
		wantAlert bool
	}{
		{name: "minimum tolerance", upstream: 1_000_000, recorded: 1_040_000, wantAlert: false},
		{name: "above minimum", upstream: 1_000_000, recorded: 1_060_000, wantAlert: true},
		{name: "two percent tolerance", upstream: 10_000_000, recorded: 10_190_000, wantAlert: false},
		{name: "above two percent", upstream: 10_000_000, recorded: 10_210_000, wantAlert: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := reconcileAISAUsage(
				"2026-10-29",
				[]aisaUsageBucket{{StartTime: day, ChargedMicrosUSD: test.upstream}},
				map[string]int64{"2026-10-29": test.recorded},
			)
			if result.Alert != test.wantAlert {
				t.Fatalf("unexpected alert result: %#v", result)
			}
		})
	}
}
