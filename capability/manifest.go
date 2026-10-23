package capability

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	ModeSync  = "sync"
	ModeAsync = "async"

	BillingPerCall = "per_call"
	BillingPerUnit = "per_unit"
)

type Manifest struct {
	ID          string           `json:"id"`
	Provider    string           `json:"provider"`
	Mode        string           `json:"mode"`
	Description string           `json:"description"`
	Endpoint    string           `json:"endpoint,omitempty"`
	Method      string           `json:"method,omitempty"`
	Auth        *AuthSpec        `json:"auth,omitempty"`
	Request     *RequestSpec     `json:"request,omitempty"`
	Response    *ResponseSpec    `json:"response,omitempty"`
	Submit      *SubmitSpec      `json:"submit,omitempty"`
	Poll        *PollSpec        `json:"poll,omitempty"`
	Content     *ContentSpec     `json:"content,omitempty"`
	Billing     BillingSpec      `json:"billing"`
	Limits      LimitsSpec       `json:"limits"`
	Retry       RetrySpec        `json:"retry"`
	Idempotency *IdempotencySpec `json:"idempotency,omitempty"`
}

type AuthSpec struct {
	Type      string `json:"type"` // bearer | header | query
	SecretRef string `json:"secret_ref"`
	Header    string `json:"header,omitempty"`
	Query     string `json:"query,omitempty"`
}

type RequestSpec struct {
	ContentType string                  `json:"content_type,omitempty"`
	Query       map[string]string       `json:"query,omitempty"`
	Body        map[string]string       `json:"body,omitempty"`
	Defaults    map[string]any          `json:"defaults,omitempty"`
	Consts      map[string]string       `json:"consts,omitempty"`
	Env         map[string]string       `json:"env,omitempty"`
	Required    []string                `json:"required,omitempty"`
	URLInputs   map[string]URLInputSpec `json:"url_inputs,omitempty"`
}

type URLInputSpec struct {
	AllowedHosts []string `json:"allowed_hosts"`
}

type SubmitSpec struct {
	Endpoint  string            `json:"endpoint"`
	Method    string            `json:"method"`
	Auth      *AuthSpec         `json:"auth,omitempty"`
	Query     map[string]string `json:"query,omitempty"`
	Body      map[string]string `json:"body,omitempty"`
	Defaults  map[string]any    `json:"defaults,omitempty"`
	Consts    map[string]string `json:"consts,omitempty"`
	JobIDPath string            `json:"job_id_path"`
}

type PollSpec struct {
	Endpoint      string            `json:"endpoint"`
	Method        string            `json:"method"`
	Auth          *AuthSpec         `json:"auth,omitempty"`
	Query         map[string]string `json:"query,omitempty"`
	Body          map[string]string `json:"body,omitempty"`
	Defaults      map[string]any    `json:"defaults,omitempty"`
	Consts        map[string]string `json:"consts,omitempty"`
	StatusPath    string            `json:"status_path"`
	SuccessValues []string          `json:"success_values"`
	FailureValues []string          `json:"failure_values"`
	ResultPath    string            `json:"result_path"`
	Interval      string            `json:"interval,omitempty"`
}

type ContentSpec struct {
	Endpoint string            `json:"endpoint"`
	Method   string            `json:"method"`
	Auth     *AuthSpec         `json:"auth,omitempty"`
	Query    map[string]string `json:"query,omitempty"`
	Consts   map[string]string `json:"consts,omitempty"`
}

type ResponseSpec struct {
	Output       map[string]string `json:"output"`
	Success      *SuccessSpec      `json:"success,omitempty"`
	StatusHeader string            `json:"status_header,omitempty"`
	ErrorHeader  string            `json:"error_header,omitempty"`
}

type SuccessSpec struct {
	Path        string `json:"path,omitempty"`
	Values      []any  `json:"values,omitempty"`
	NonEmpty    string `json:"non_empty,omitempty"`
	FailureCode string `json:"failure_code,omitempty"`
	EmptyCode   string `json:"empty_code,omitempty"`
}

type BillingSpec struct {
	Type         string  `json:"type"`
	Unit         string  `json:"unit,omitempty"`
	PriceUSD     float64 `json:"price_usd"`
	QuantityPath string  `json:"quantity_path,omitempty"`
	EstimatePath string  `json:"estimate_path,omitempty"`
}

type LimitsSpec struct {
	Timeout          string `json:"timeout"`
	MaxResponseBytes int64  `json:"max_response_bytes"`
}

type RetrySpec struct {
	Attempts int   `json:"attempts"`
	RetryOn  []int `json:"retry_on"`
}

type IdempotencySpec struct {
	Header string `json:"header,omitempty"`
	TTL    string `json:"ttl,omitempty"`
}

func (m *Manifest) Validate() error {
	if strings.TrimSpace(m.ID) == "" {
		return fmt.Errorf("manifest id is required")
	}
	if m.Mode != ModeSync && m.Mode != ModeAsync {
		return fmt.Errorf("manifest %s: mode must be sync or async", m.ID)
	}
	if m.Billing.Type != BillingPerCall && m.Billing.Type != BillingPerUnit {
		return fmt.Errorf("manifest %s: unsupported billing type %q", m.ID, m.Billing.Type)
	}
	if m.Billing.PriceUSD < 0 {
		return fmt.Errorf("manifest %s: price_usd cannot be negative", m.ID)
	}
	if m.Billing.Type == BillingPerUnit && strings.TrimSpace(m.Billing.QuantityPath) == "" {
		return fmt.Errorf("manifest %s: per_unit billing requires quantity_path", m.ID)
	}
	if m.Mode == ModeAsync && m.Billing.Type == BillingPerUnit &&
		strings.TrimSpace(m.Billing.EstimatePath) == "" {
		return fmt.Errorf("manifest %s: async per_unit billing requires estimate_path", m.ID)
	}
	if _, err := m.Timeout(); err != nil {
		return fmt.Errorf("manifest %s: %w", m.ID, err)
	}
	if m.Mode == ModeSync {
		if strings.TrimSpace(m.Endpoint) == "" {
			return fmt.Errorf("manifest %s: sync endpoint is required", m.ID)
		}
		if m.Response == nil || len(m.Response.Output) == 0 {
			return fmt.Errorf("manifest %s: sync response.output is required", m.ID)
		}
		if m.Request != nil {
			for _, field := range m.Request.Required {
				if strings.TrimSpace(field) == "" {
					return fmt.Errorf("manifest %s: request.required contains an empty field", m.ID)
				}
			}
			for field, envName := range m.Request.Env {
				if strings.TrimSpace(field) == "" || strings.TrimSpace(envName) == "" {
					return fmt.Errorf("manifest %s: request.env contains an empty field or variable", m.ID)
				}
			}
			for field, spec := range m.Request.URLInputs {
				if strings.TrimSpace(field) == "" || len(spec.AllowedHosts) == 0 {
					return fmt.Errorf("manifest %s: request.url_inputs.%s requires allowed_hosts", m.ID, field)
				}
				for _, host := range spec.AllowedHosts {
					if strings.TrimSpace(host) == "" {
						return fmt.Errorf("manifest %s: request.url_inputs.%s contains an empty host", m.ID, field)
					}
				}
			}
		}
		if m.Response.Success != nil {
			success := m.Response.Success
			if strings.TrimSpace(success.Path) == "" && strings.TrimSpace(success.NonEmpty) == "" {
				return fmt.Errorf("manifest %s: response.success requires path or non_empty", m.ID)
			}
			if strings.TrimSpace(success.Path) != "" && len(success.Values) == 0 {
				return fmt.Errorf("manifest %s: response.success.path requires values", m.ID)
			}
		}
	} else {
		if m.Submit == nil || strings.TrimSpace(m.Submit.Endpoint) == "" || m.Submit.JobIDPath == "" {
			return fmt.Errorf("manifest %s: async submit.endpoint and job_id_path are required", m.ID)
		}
		if m.Poll == nil || strings.TrimSpace(m.Poll.Endpoint) == "" ||
			m.Poll.StatusPath == "" || m.Poll.ResultPath == "" {
			return fmt.Errorf("manifest %s: async poll.endpoint, status_path and result_path are required", m.ID)
		}
	}
	return nil
}

func (m *Manifest) Timeout() (time.Duration, error) {
	raw := strings.TrimSpace(m.Limits.Timeout)
	if raw == "" {
		return 30 * time.Second, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid timeout %q", raw)
	}
	if d <= 0 || d > 10*time.Minute {
		return 0, fmt.Errorf("timeout must be between 1s and 10m")
	}
	return d, nil
}

// Lookup resolves a simple JSON path such as $.input.latitude or $.endpoints.
// It intentionally supports only object keys and array indexes; no arbitrary
// expression or code execution.
func Lookup(root any, path string) (any, bool) {
	p := strings.TrimSpace(path)
	p = strings.TrimPrefix(p, "$")
	p = strings.TrimPrefix(p, ".")
	if p == "" {
		return root, true
	}
	cur := root
	for _, seg := range strings.Split(p, ".") {
		switch v := cur.(type) {
		case map[string]any:
			next, ok := v[seg]
			if !ok {
				return nil, false
			}
			cur = next
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(v) {
				return nil, false
			}
			cur = v[idx]
		default:
			return nil, false
		}
	}
	return cur, true
}

func StringValue(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case bool:
		if x {
			return "true", true
		}
		return "false", true
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), true
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32), true
	case int:
		return strconv.Itoa(x), true
	case int64:
		return strconv.FormatInt(x, 10), true
	case int32:
		return strconv.FormatInt(int64(x), 10), true
	case uint64:
		return strconv.FormatUint(x, 10), true
	default:
		return "", false
	}
}

func NumberValue(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case int32:
		return float64(x), true
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	default:
		return 0, false
	}
}
