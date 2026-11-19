package capability

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
)

const relaySelfKeyFixture = "test-relay-self-key"

type relayAIAnswerCapability struct {
	id         string
	model      string
	priceUSD   float64
	reserveUSD float64
}

func relayAIAnswerCapabilities() []relayAIAnswerCapability {
	return []relayAIAnswerCapability{
		{id: "ai-answer-grok-v1", model: "grok-4.7", priceUSD: 2.5e-7, reserveUSD: 0.06},
		{id: "ai-answer-kimi-v1", model: "kimi-k3", priceUSD: 8e-7, reserveUSD: 0.03},
		{id: "ai-answer-doubao-v1", model: "doubao-seed-2.1-pro", priceUSD: 5e-7, reserveUSD: 0.03},
	}
}

func cloneRelayAIAnswerManifest(t *testing.T, id string, endpoint string) *Manifest {
	t.Helper()
	embedded, ok := Get(id)
	if !ok {
		t.Fatalf("%s manifest is missing", id)
	}
	raw, err := common.Marshal(embedded)
	if err != nil {
		t.Fatalf("clone %s: %v", id, err)
	}
	var manifest Manifest
	if err := common.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("clone %s: %v", id, err)
	}
	manifest.Endpoint = endpoint
	return &manifest
}

func setRelaySelfKey(t *testing.T) {
	t.Helper()
	t.Setenv("CAPABILITY_RELAY_SELF_KEY", relaySelfKeyFixture)
}

func readRelayCompletionFixture(t *testing.T, withUsage bool, status string) []byte {
	t.Helper()
	usage := ""
	if withUsage {
		usage = `,"usage":{"input_tokens":400,"output_tokens":100,"total_tokens":500}`
	}
	return []byte(`{"id":"resp_fixture","object":"response","status":"` + status + `","model":"fixture-model",` +
		`"output":[{"type":"web_search_call","id":"ws_1","status":"completed"},` +
		`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"fixture answer",` +
		`"annotations":[{"type":"url_citation","url":"https://example.com/a","title":"A"}]}]}]` + usage + `}`)
}

func TestRelayAIAnswerManifestContracts(t *testing.T) {
	for _, test := range relayAIAnswerCapabilities() {
		t.Run(test.id, func(t *testing.T) {
			m, ok := Get(test.id)
			if !ok {
				t.Fatalf("%s manifest is missing", test.id)
			}
			if err := m.Validate(); err != nil {
				t.Fatalf("%s should validate: %v", test.id, err)
			}
			if m.Provider != "relay" || m.Mode != ModeSync ||
				m.Endpoint != "https://api.tokwork.com/v1/responses" ||
				m.Method != http.MethodPost || m.Native != "" {
				t.Fatalf("unexpected %s contract: %#v", test.id, m)
			}
			if m.Auth == nil || m.Auth.Type != "bearer" ||
				m.Auth.SecretRef != "CAPABILITY_RELAY_SELF_KEY" {
				t.Fatalf("unexpected %s auth: %#v", test.id, m.Auth)
			}
			if m.Request == nil || len(m.Request.Required) != 1 || m.Request.Required[0] != "prompt" {
				t.Fatalf("unexpected %s request contract: %#v", test.id, m.Request)
			}
			if m.Request.Body["input"] != "$.input.prompt" ||
				m.Request.Body["instructions"] != "$.input.system_message" ||
				m.Request.Body["max_output_tokens"] != "$.input.max_output_tokens" {
				t.Fatalf("unexpected %s body mapping: %#v", test.id, m.Request.Body)
			}
			if model, ok := m.Request.Consts["model"].(string); !ok || model != test.model {
				t.Fatalf("%s must pin the model constant, got %#v", test.id, m.Request.Consts["model"])
			}
			if !strings.Contains(m.Description, test.model) {
				t.Fatalf("%s description must name its model %s", test.id, test.model)
			}
			tools, ok := m.Request.Consts["tools"].([]any)
			if !ok || len(tools) != 1 {
				t.Fatalf("%s must send exactly one tool, got %#v", test.id, m.Request.Consts["tools"])
			}
			tool, ok := tools[0].(map[string]any)
			if !ok || tool["type"] != "web_search" {
				t.Fatalf("%s must send the web_search tool, got %#v", test.id, tools[0])
			}
			if def, ok := m.Request.Defaults["max_output_tokens"]; !ok || !responseValuesEqual(def, 1500) {
				t.Fatalf("%s must default max_output_tokens to 1500, got %#v", test.id, m.Request.Defaults)
			}
			rng, ok := m.Request.Ranges["max_output_tokens"]
			if !ok || rng.Min == nil || rng.Max == nil || *rng.Min != 200 || *rng.Max != 4000 || !rng.Integer {
				t.Fatalf("%s max_output_tokens range is wrong: %#v", test.id, rng)
			}
			if m.Response == nil || m.Response.Success == nil ||
				m.Response.Success.Path != "$.status" ||
				len(m.Response.Success.Values) != 2 ||
				!responseValuesEqual(m.Response.Success.Values[0], "completed") ||
				!responseValuesEqual(m.Response.Success.Values[1], "incomplete") {
				t.Fatalf("unexpected %s success contract: %#v", test.id, m.Response)
			}
			if m.Response.Output["result"] != "$.output" ||
				m.Response.Output["output_text"] != "$.output_text" ||
				m.Response.Output["usage"] != "$.usage" ||
				m.Response.Output["model"] != "$.model" {
				t.Fatalf("unexpected %s output contract: %#v", test.id, m.Response.Output)
			}
			for _, key := range []string{"output_text", "usage", "model"} {
				if !m.Response.OptionalOutputContains(key) {
					t.Fatalf("%s must treat %s as optional", test.id, key)
				}
			}
			if m.Response.OptionalOutputContains("result") {
				t.Fatalf("%s must keep result required", test.id)
			}
			for _, code := range []string{"400", "401", "403", "429", "500", "502", "503", "504"} {
				if _, ok := m.Response.ErrorMap[code]; !ok {
					t.Fatalf("%s is missing error_map entry %s", test.id, code)
				}
			}
			if m.Billing.Type != BillingPerUnit || m.Billing.Unit != "token" ||
				m.Billing.PriceUSD != test.priceUSD ||
				m.Billing.QuantityPath != "$.usage.total_tokens" ||
				m.Billing.ReserveUSD != test.reserveUSD {
				t.Fatalf("unexpected %s billing fields: %#v", test.id, m.Billing)
			}
			timeout, err := m.Timeout()
			if err != nil || timeout.Seconds() != 115 {
				t.Fatalf("unexpected %s timeout: %v %v", test.id, timeout, err)
			}
			if m.Retry.Attempts != 1 {
				t.Fatalf("%s must not retry, got %#v", test.id, m.Retry)
			}
		})
	}
}

func TestRelayAIAnswerRequestBodiesAndAuth(t *testing.T) {
	allowLocalTestUpstreams(t)
	setRelaySelfKey(t)

	tests := []struct {
		name    string
		input   map[string]any
		want    []string
		notWant []string
	}{
		{
			name:  "prompt only omits optional fields",
			input: map[string]any{"prompt": "best wooden toy set"},
			want: []string{
				`"input":"best wooden toy set"`,
				`"max_output_tokens":1500`,
			},
			notWant: []string{"instructions", "system_message"},
		},
		{
			name: "explicit optional fields are forwarded",
			input: map[string]any{
				"prompt":            "best wooden toy set",
				"system_message":    "answer briefly",
				"max_output_tokens": 900,
			},
			want: []string{
				`"instructions":"answer briefly"`,
				`"max_output_tokens":900`,
			},
		},
	}
	for _, test := range tests {
		for _, capability := range relayAIAnswerCapabilities() {
			t.Run(capability.id+"/"+test.name, func(t *testing.T) {
				var actualRequest string
				var gotAuth string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost {
						t.Errorf("unexpected method %s", r.Method)
					}
					gotAuth = r.Header.Get("Authorization")
					raw, err := io.ReadAll(r.Body)
					if err != nil {
						t.Errorf("read body: %v", err)
					}
					actualRequest = string(raw)
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write(readRelayCompletionFixture(t, true, "completed"))
				}))
				defer server.Close()

				manifest := cloneRelayAIAnswerManifest(t, capability.id, server.URL)
				result, execErr := ExecuteSync(context.Background(), manifest, test.input, "")
				if execErr != nil {
					t.Fatalf("%s failed: %v", capability.id, execErr)
				}
				if gotAuth != "Bearer "+relaySelfKeyFixture {
					t.Fatalf("%s sent an unexpected Authorization header: %q", capability.id, gotAuth)
				}
				for _, want := range []string{
					`"model":"` + capability.model + `"`,
					`"tools":[{"type":"web_search"}]`,
				} {
					if !strings.Contains(actualRequest, want) {
						t.Fatalf("%s request body is missing %s: %s", capability.id, want, actualRequest)
					}
				}
				for _, want := range test.want {
					if !strings.Contains(actualRequest, want) {
						t.Fatalf("%s request body is missing %s: %s", capability.id, want, actualRequest)
					}
				}
				for _, notWant := range test.notWant {
					if strings.Contains(actualRequest, notWant) {
						t.Fatalf("%s request body must not contain %s: %s", capability.id, notWant, actualRequest)
					}
				}

				outputs, ok := result.Output["result"].([]any)
				if !ok || len(outputs) != 2 {
					t.Fatalf("%s must return the whole output array: %#v", capability.id, result.Output)
				}
				if result.Output["output_text"] != nil {
					t.Fatalf("%s must map a missing optional path to null: %#v", capability.id, result.Output)
				}
				usage, ok := result.Output["usage"].(map[string]any)
				if !ok || !responseValuesEqual(usage["total_tokens"], 500) {
					t.Fatalf("%s must expose usage: %#v", capability.id, result.Output["usage"])
				}
				if result.Output["model"] != "fixture-model" {
					t.Fatalf("%s must expose model: %#v", capability.id, result.Output["model"])
				}
			})
		}
	}
}

func TestRelayAIAnswerAcceptsIncompleteStatus(t *testing.T) {
	allowLocalTestUpstreams(t)
	setRelaySelfKey(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"incomplete","output":[{"type":"message"}],` +
			`"output_text":"partial","usage":{"total_tokens":42},"model":"grok-4.7"}`))
	}))
	defer server.Close()

	manifest := cloneRelayAIAnswerManifest(t, "ai-answer-grok-v1", server.URL)
	result, execErr := ExecuteSync(context.Background(), manifest, map[string]any{"prompt": "hi"}, "")
	if execErr != nil {
		t.Fatalf("incomplete response must succeed: %v", execErr)
	}
	if result.Output["output_text"] != "partial" {
		t.Fatalf("unexpected output: %#v", result.Output)
	}
}

func TestRelayAIAnswerUpstreamErrorsAreMapped(t *testing.T) {
	allowLocalTestUpstreams(t)
	setRelaySelfKey(t)

	tests := []struct {
		name       string
		status     int
		body       string
		wantStatus int
		wantCode   string
		wantLog    string
	}{
		{
			name:       "unauthorized",
			status:     http.StatusUnauthorized,
			body:       `{"error":{"message":"secret auth detail","type":"invalid_request_error","code":"invalid_api_key"}}`,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "upstream_unavailable",
			wantLog:    "upstream_authentication",
		},
		{
			name:       "forbidden",
			status:     http.StatusForbidden,
			body:       `{"error":{"message":"secret model detail","type":"invalid_request_error"}}`,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "upstream_unavailable",
			wantLog:    "upstream_authentication",
		},
		{
			name:       "rate limited",
			status:     http.StatusTooManyRequests,
			body:       `{"error":{"message":"secret limit detail","type":"rate_limit_error"}}`,
			wantStatus: http.StatusTooManyRequests,
			wantCode:   "upstream_rate_limited",
			wantLog:    "upstream_rate_limited",
		},
		{
			name:       "invalid request",
			status:     http.StatusBadRequest,
			body:       `{"error":{"message":"secret parameter detail","type":"invalid_request_error","code":"invalid_value"}}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
			wantLog:    "upstream_invalid_request",
		},
		{
			name:       "provider failure",
			status:     http.StatusInternalServerError,
			body:       `{"error":{"message":"secret provider detail","type":"server_error"}}`,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "upstream_unavailable",
			wantLog:    "upstream_provider_failure",
		},
		{
			name:       "upstream unavailable",
			status:     http.StatusBadGateway,
			body:       `{"error":{"message":"secret gateway detail","type":"server_error"}}`,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "upstream_unavailable",
			wantLog:    "upstream_provider_failure",
		},
		{
			name:       "gateway timeout",
			status:     http.StatusGatewayTimeout,
			body:       `{"error":{"message":"secret timeout detail","type":"server_error"}}`,
			wantStatus: http.StatusGatewayTimeout,
			wantCode:   "upstream_timeout",
			wantLog:    "upstream_downstream_timeout",
		},
		{
			name:       "failed status inside a 200",
			status:     http.StatusOK,
			body:       `{"status":"failed","output":[],"error":{"code":"server_error","message":"secret status detail"}}`,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "upstream_unavailable",
			wantLog:    "upstream_provider_failure",
		},
	}
	for _, test := range tests {
		for _, capability := range relayAIAnswerCapabilities() {
			t.Run(capability.id+"/"+test.name, func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(test.status)
					_, _ = w.Write([]byte(test.body))
				}))
				defer server.Close()

				manifest := cloneRelayAIAnswerManifest(t, capability.id, server.URL)
				_, execErr := ExecuteSync(context.Background(), manifest, map[string]any{"prompt": "hi"}, "")
				if execErr == nil || execErr.Code != test.wantCode ||
					execErr.HTTPStatus != test.wantStatus || execErr.LogCode != test.wantLog {
					t.Fatalf("%s unexpected error: %#v", capability.id, execErr)
				}
				if calls != 1 {
					t.Fatalf("%s made %d upstream calls, want 1 (no retries)", capability.id, calls)
				}
				for _, secret := range []string{"secret", relaySelfKeyFixture, "Authorization"} {
					if strings.Contains(execErr.Message+execErr.Error(), secret) {
						t.Fatalf("%s leaked a sensitive value: %v", capability.id, execErr)
					}
				}
			})
		}
	}
}

func TestRelayAIAnswerMissingCredentialFailsBeforeUpstream(t *testing.T) {
	t.Setenv("CAPABILITY_RELAY_SELF_KEY", "")

	for _, capability := range relayAIAnswerCapabilities() {
		m, ok := Get(capability.id)
		if !ok {
			t.Fatalf("%s manifest is missing", capability.id)
		}
		execErr := ValidateSync(m, map[string]any{"prompt": "hi"})
		if execErr == nil || execErr.Code != "credential_error" ||
			execErr.HTTPStatus != http.StatusInternalServerError {
			t.Fatalf("%s expected credential_error, got %#v", capability.id, execErr)
		}
	}
}

func TestRelayAIAnswerRequiredPromptIsValidated(t *testing.T) {
	setRelaySelfKey(t)

	for _, capability := range relayAIAnswerCapabilities() {
		m, ok := Get(capability.id)
		if !ok {
			t.Fatalf("%s manifest is missing", capability.id)
		}
		for name, input := range map[string]map[string]any{
			"missing": {},
			"empty":   {"prompt": "   "},
		} {
			t.Run(capability.id+"/"+name, func(t *testing.T) {
				execErr := ValidateSync(m, input)
				if execErr == nil || execErr.Code != "invalid_request" ||
					execErr.HTTPStatus != http.StatusBadRequest {
					t.Fatalf("%s expected invalid_request, got %#v", capability.id, execErr)
				}
			})
		}
		for _, invalid := range []any{199, 4001, 1500.5, "1500"} {
			execErr := ValidateSync(m, map[string]any{"prompt": "hi", "max_output_tokens": invalid})
			if execErr == nil || execErr.Code != "invalid_request" {
				t.Fatalf("%s expected invalid_request for %#v, got %#v", capability.id, invalid, execErr)
			}
		}
		if execErr := ValidateSync(m, map[string]any{"prompt": "hi", "max_output_tokens": 200}); execErr != nil {
			t.Fatalf("%s should accept the lower bound: %v", capability.id, execErr)
		}
		if execErr := ValidateSync(m, map[string]any{"prompt": "hi", "max_output_tokens": 4000}); execErr != nil {
			t.Fatalf("%s should accept the upper bound: %v", capability.id, execErr)
		}
	}
}

func TestOptionalOutputRequiresDeclaredKey(t *testing.T) {
	base := &Manifest{
		ID:       "optional-output-check-v1",
		Provider: "test",
		Mode:     ModeSync,
		Endpoint: "https://example.com/v1",
		Response: &ResponseSpec{
			Output:         map[string]string{"result": "$.result"},
			OptionalOutput: []string{"missing"},
		},
		Billing: BillingSpec{Type: BillingPerCall, PriceUSD: 0},
		Limits:  LimitsSpec{Timeout: "5s"},
	}
	if err := base.Validate(); err == nil {
		t.Fatal("optional_output must reject keys that are not declared in output")
	}
	base.Response.OptionalOutput = []string{"result", "result"}
	if err := base.Validate(); err == nil {
		t.Fatal("optional_output must reject duplicate keys")
	}
	base.Response.OptionalOutput = []string{"result"}
	if err := base.Validate(); err != nil {
		t.Fatalf("optional_output should accept a declared key: %v", err)
	}
}
