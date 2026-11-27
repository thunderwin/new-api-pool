package capability

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
)

func TestDouyinSearchContract(t *testing.T) {
	const secret = "synthetic-tikhub-credential"
	t.Setenv("TIKHUB_API_KEY", secret)
	t.Setenv("CAPABILITY_ALLOW_INSECURE_URLS", "1")
	t.Setenv("CAPABILITY_ALLOW_PRIVATE_URLS", "1")
	fixture, err := os.ReadFile("testdata/tikhub-douyin-search-success.json")
	if err != nil {
		t.Fatal(err)
	}
	fixture = []byte(strings.ReplaceAll(string(fixture), "合成视频", strings.Repeat("测🙂", 61)))
	embedded, ok := Get("douyin-search-v1")
	const path = "/api/v1/douyin/search/fetch_video_search_v2"
	if !ok || embedded.Endpoint != "https://api.tikhub.io"+path || embedded.Auth.SecretRef != "TIKHUB_API_KEY" {
		t.Fatal("incorrect production upstream configuration")
	}
	first := map[string]any{"keyword": strings.Repeat("测", 50), "cursor": float64(0), "search_id": "", "sort_type": "0", "publish_time": "0", "filter_duration": "0", "content_type": "1", "backtrace": ""}
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if r.Method != http.MethodPost || r.URL.RequestURI() != path || r.Header.Get("Content-Type") != "application/json" ||
			r.Header.Get("Authorization") != "Bearer "+secret || r.Header.Get("Idempotency-Key") != "" {
			t.Error("wrong request method, path, headers, or unexpected idempotency forwarding")
		}
		var body map[string]any
		if err := common.DecodeJson(r.Body, &body); err != nil {
			t.Error(err)
		}
		expected := first
		if n > 1 {
			expected = map[string]any{}
			for k, v := range first {
				expected[k] = v
			}
			expected["cursor"], expected["search_id"] = float64(18), "synthetic-next"
		}
		if !reflect.DeepEqual(body, expected) {
			t.Error("unexpected upstream search JSON")
		}
		if n == 1 {
			_, _ = w.Write(fixture)
		} else {
			fmt.Fprint(w, `{"code":200,"data":{"business_data":null,"cursor":18,"search_id":"synthetic-final","has_more":0}}`)
		}
	}))
	defer upstream.Close()
	m := *embedded
	m.Endpoint = upstream.URL + path
	result, execErr := ExecuteSync(context.Background(), &m, map[string]any{
		"keyword": first["keyword"], "content_type": "2", "backtrace": "not allowed", "Authorization": "not allowed",
	}, "synthetic-idempotency")
	if execErr != nil || result.Empty {
		t.Fatalf("expected nonempty result, got %v", execErr)
	}
	wantItems := []map[string]any{
		{"id": "synthetic-video-a", "desc": strings.Repeat("测🙂", 60), "author": "合成作者", "duration_s": 12.345, "likes": float64(42), "share_url": "https://share.example.invalid/video-a", "play_url": "https://play.example.invalid/high", "width": float64(1080), "height": float64(1920)},
		{"id": "synthetic-video-b", "desc": "", "author": "", "duration_s": nil, "likes": nil, "share_url": "", "play_url": "https://play.example.invalid/default-b", "width": float64(640), "height": float64(480)},
	}
	if !reflect.DeepEqual(result.Output["items"], wantItems) || result.Output["has_more"] != float64(1) || result.Output["cursor"] != float64(18) || result.Output["search_id"] != "synthetic-next" {
		t.Fatalf("unexpected search projection: %#v", result.Output)
	}
	// Sync requests without an upstream idempotency declaration are independent,
	// even when callers send the same key concurrently.
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			page, err := ExecuteSync(context.Background(), &m, map[string]any{
				"keyword": first["keyword"], "cursor": result.Output["cursor"], "search_id": result.Output["search_id"],
			}, "synthetic-idempotency")
			if err != nil || !page.Empty || page.Output["search_id"] != "synthetic-final" || page.Output["cursor"] != float64(18) || page.Output["has_more"] != float64(0) {
				t.Errorf("expected empty next page with pagination: page=%v err=%v", page, err)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 5 {
		t.Fatalf("expected one upstream call per invocation, got %d", calls.Load())
	}
}

func TestDouyinSearchSSRF(t *testing.T) {
	t.Setenv("TIKHUB_API_KEY", "synthetic-ssrf-credential")
	t.Setenv("CAPABILITY_ALLOW_INSECURE_URLS", "1")
	t.Setenv("CAPABILITY_ALLOW_PRIVATE_URLS", "")
	embedded, ok := Get("douyin-search-v1")
	if !ok {
		t.Fatal("missing embedded manifest")
	}
	m := *embedded
	m.Endpoint = "http://127.0.0.1:1"
	if _, err := ExecuteSync(context.Background(), &m, map[string]any{"keyword": "测试"}, ""); err == nil || err.Code != "upstream_not_allowed" {
		t.Fatalf("private endpoint was not blocked: %v", err)
	}
	t.Setenv("CAPABILITY_ALLOW_PRIVATE_URLS", "1") // Mock only.
	var followed atomic.Bool
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/target" {
			followed.Store(true)
			return
		}
		w.Header().Set("Location", "/target")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	m.Endpoint = redirect.URL
	if _, err := ExecuteSync(context.Background(), &m, map[string]any{"keyword": "测试"}, ""); err == nil || followed.Load() {
		t.Fatal("upstream redirects must not be followed")
	}
}
