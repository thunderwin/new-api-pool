package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/capability"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"

	"github.com/bytedance/gopkg/util/gopool"
)

const (
	aisaReconcileMaxBytes    = 1 << 20
	aisaReconcileMinDeltaUSD = 0.05
	aisaReconcilePercent     = 0.02
)

var aisaUsageEndpoint = "https://api.aisa.one/v1/usage"

type aisaUsageBucket struct {
	StartTime        int64 `json:"start_time"`
	EndTime          int64 `json:"end_time"`
	ChargedMicrosUSD int64 `json:"charged_micros_usd"`
}

type aisaUsageResponse struct {
	Currency string            `json:"currency"`
	Scope    string            `json:"scope"`
	Buckets  []aisaUsageBucket `json:"buckets"`
}

// AISAReconciliation is intentionally content-free: only the UTC date, the
// upstream charged amount and the recorded capability cost are retained.
type AISAReconciliation struct {
	Date                string
	UpstreamMicrosUSD   int64
	RecordedMicrosUSD   int64
	DifferenceMicrosUSD int64
	Alert               bool
}

// StartAISACapabilityReconcileTask reconciles the previous UTC day once it is
// available. AISA usage buckets are explicitly documented as UTC daily buckets.
func StartAISACapabilityReconcileTask(ctx context.Context) {
	if !common.IsMasterNode {
		return
	}
	gopool.Go(func() {
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()
		lastRun := ""
		for {
			now := time.Now().UTC()
			target := now.AddDate(0, 0, -1)
			targetDate := target.Format("2006-01-02")
			if now.Hour() >= 1 && lastRun != targetDate {
				if err := reconcileAISAPreviousDay(ctx, targetDate); err != nil {
					common.SysError(fmt.Sprintf("WARN capability AISA reconciliation date=%s error=%v", targetDate, err))
				} else {
					lastRun = targetDate
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
}

func reconcileAISAPreviousDay(ctx context.Context, date string) error {
	key := strings.TrimSpace(os.Getenv("CAPABILITY_AISA_API_KEY"))
	if key == "" {
		return fmt.Errorf("CAPABILITY_AISA_API_KEY is not configured")
	}
	day, err := time.ParseInLocation("2006-01-02", date, time.UTC)
	if err != nil {
		return err
	}
	buckets, err := fetchAISAUsage(ctx, key, day, day.AddDate(0, 0, 1))
	if err != nil {
		return err
	}
	recorded, err := sumRecordedAISAUsage(day, day.AddDate(0, 0, 1))
	if err != nil {
		return err
	}
	result := reconcileAISAUsage(date, buckets, recorded)
	common.SysLog(fmt.Sprintf("capability AISA reconciliation date=%s upstream_usd=%.6f recorded_usd=%.6f difference_usd=%.6f",
		result.Date,
		float64(result.UpstreamMicrosUSD)/1_000_000,
		float64(result.RecordedMicrosUSD)/1_000_000,
		float64(result.DifferenceMicrosUSD)/1_000_000,
	))
	if result.Alert {
		content := fmt.Sprintf("date=%s upstream_usd=%.6f recorded_usd=%.6f difference_usd=%.6f",
			result.Date,
			float64(result.UpstreamMicrosUSD)/1_000_000,
			float64(result.RecordedMicrosUSD)/1_000_000,
			float64(result.DifferenceMicrosUSD)/1_000_000,
		)
		common.SysError("WARN capability AISA reconciliation mismatch " + content)
		NotifyRootUser(dto.NotifyTypeCapabilityReconcile, "AISA capability billing mismatch", content)
	}
	return nil
}

func fetchAISAUsage(ctx context.Context, key string, start, end time.Time) ([]aisaUsageBucket, error) {
	query := url.Values{}
	query.Set("start_time", strconv.FormatInt(start.UTC().Unix(), 10))
	query.Set("end_time", strconv.FormatInt(end.UTC().Unix(), 10))
	query.Set("bucket_width", "1d")
	query.Set("scope", "key")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, aisaUsageEndpoint+"?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+key)
	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("usage request failed: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, aisaReconcileMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("usage response read failed: %w", err)
	}
	if len(body) > aisaReconcileMaxBytes {
		return nil, fmt.Errorf("usage response exceeded limit")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("usage request returned HTTP %d", response.StatusCode)
	}
	var payload aisaUsageResponse
	if err := common.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("usage response JSON is invalid: %w", err)
	}
	if payload.Currency != "" && payload.Currency != "USD" {
		return nil, fmt.Errorf("usage currency is not USD")
	}
	if payload.Scope != "" && payload.Scope != "key" {
		return nil, fmt.Errorf("usage scope is not key")
	}
	return payload.Buckets, nil
}

func sumRecordedAISAUsage(start, end time.Time) (map[string]int64, error) {
	var rows []struct {
		CreatedAt int64
		Other     string
	}
	if err := model.LOG_DB.Model(&model.Log{}).
		Select("created_at, other").
		Where("type = ? AND created_at >= ? AND created_at < ?", model.LogTypeConsume, start.UTC().Unix(), end.UTC().Unix()).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[string]int64)
	for _, row := range rows {
		var other map[string]any
		if err := common.UnmarshalJsonStr(row.Other, &other); err != nil {
			continue
		}
		provider, _ := other["provider"].(string)
		unit, _ := other["usage_unit"].(string)
		if provider != "aisa" || unit != "micro_usd" {
			continue
		}
		quantity, ok := capability.NumberValue(other["usage_quantity"])
		if !ok || quantity <= 0 {
			continue
		}
		date := time.Unix(row.CreatedAt, 0).UTC().Format("2006-01-02")
		result[date] += int64(quantity + 0.5)
	}
	return result, nil
}

func reconcileAISAUsage(date string, buckets []aisaUsageBucket, recorded map[string]int64) AISAReconciliation {
	result := AISAReconciliation{Date: date, RecordedMicrosUSD: recorded[date]}
	for _, bucket := range buckets {
		bucketDate := time.Unix(bucket.StartTime, 0).UTC().Format("2006-01-02")
		if bucketDate == date {
			result.UpstreamMicrosUSD += bucket.ChargedMicrosUSD
		}
	}
	result.DifferenceMicrosUSD = result.RecordedMicrosUSD - result.UpstreamMicrosUSD
	threshold := int64(aisaReconcileMinDeltaUSD * 1_000_000)
	percentage := int64(float64(absInt64(result.UpstreamMicrosUSD)) * aisaReconcilePercent)
	if percentage > threshold {
		threshold = percentage
	}
	result.Alert = absInt64(result.DifferenceMicrosUSD) > threshold
	return result
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
