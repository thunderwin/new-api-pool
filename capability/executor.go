package capability

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

type ExecutionError struct {
	Code              string
	Message           string
	HTTPStatus        int
	Retryable         bool
	LogCode           string
	ResponseReceived  bool
	RequestWritten    bool
	MayHaveCharged    bool
	UpstreamStatus    int
	BillingHeaders    http.Header
	BillingRaw        map[string]any
	UpstreamRequestID string
	CostUSD           float64
	Billed            bool
	UsageSource       string
}

func (e *ExecutionError) Error() string {
	if e == nil {
		return ""
	}
	return e.Code + ": " + e.Message
}

type SyncResult struct {
	Output            map[string]any
	Raw               map[string]any
	Headers           http.Header
	UpstreamRequestID string
	HTTPStatus        int
}

type AsyncSubmitResult struct {
	JobID             string
	InitialStatus     string
	UpstreamRequestID string
	Raw               map[string]any
}

type AsyncPollResult struct {
	Status            string
	Completed         bool
	Result            any
	Raw               map[string]any
	ErrorCode         string
	ErrorMessage      string
	UpstreamRequestID string
}

type httpSpec struct {
	endpoint    string
	method      string
	auth        *AuthSpec
	query       map[string]string
	body        map[string]string
	bodyWrap    string
	headers     map[string]string
	defaults    map[string]any
	consts      map[string]string
	env         map[string]string
	idempotency string
}

// OperationContext detaches an upstream operation from the caller connection
// while preserving request values and enforcing one overall manifest deadline.
// Once a paid request may have reached the provider, disconnects must not turn
// the operation into a free call.
func OperationContext(parent context.Context, m *Manifest) (context.Context, context.CancelFunc) {
	timeout, err := m.Timeout()
	if err != nil {
		timeout = 30 * time.Second
	}
	detached := context.WithoutCancel(parent)
	return context.WithTimeout(detached, timeout)
}

// ValidateSync checks all request-local errors that can be detected before any
// billing reservation or upstream call.
func ValidateSync(m *Manifest, input map[string]any) *ExecutionError {
	if m == nil {
		return &ExecutionError{Code: "invalid_manifest", Message: "capability manifest is missing", HTTPStatus: http.StatusInternalServerError}
	}
	if m.Request != nil {
		for _, field := range m.Request.Required {
			value, ok := input[field]
			if !ok && m.Request.Defaults != nil {
				value, ok = m.Request.Defaults[field]
			}
			if !ok || isEmptyInputValue(value) {
				return &ExecutionError{
					Code:       "invalid_request",
					Message:    "a required input field is missing or empty",
					HTTPStatus: http.StatusBadRequest,
				}
			}
		}
		for field, spec := range m.Request.URLInputs {
			value, ok := input[field]
			if !ok || validateURLInputValue(value, spec.AllowedHosts) != nil {
				return &ExecutionError{
					Code:       "invalid_request",
					Message:    "an input URL is not allowed",
					HTTPStatus: http.StatusBadRequest,
				}
			}
		}
	}
	return validateEnvironment(m)
}

func ExecuteSync(ctx context.Context, m *Manifest, input map[string]any, idempotencyKey string) (*SyncResult, *ExecutionError) {
	if execErr := ValidateSync(m, input); execErr != nil {
		return nil, execErr
	}
	spec := httpSpec{
		endpoint:    m.Endpoint,
		method:      defaultMethod(m.Method),
		auth:        m.Auth,
		query:       nil,
		body:        nil,
		idempotency: idempotencyKey,
	}
	if m.Request != nil {
		spec.query = m.Request.Query
		spec.body = m.Request.Body
		spec.bodyWrap = m.Request.BodyWrap
		spec.headers = m.Request.Headers
		spec.defaults = m.Request.Defaults
		spec.consts = m.Request.Consts
		spec.env = m.Request.Env
	}
	raw, headers, statusCode, upstreamID, execErr := doRequest(ctx, m, spec, input)
	if execErr != nil {
		return nil, execErr
	}
	output := make(map[string]any, len(m.Response.Output))
	for key, path := range m.Response.Output {
		value, ok := Lookup(raw, path)
		if !ok {
			return nil, attachResponseMetadata(&ExecutionError{
				Code:       "missing_output_field",
				Message:    "upstream response did not contain a required field",
				HTTPStatus: http.StatusBadGateway,
			}, m, statusCode, headers, raw, true, upstreamID)
		}
		output[key] = value
	}
	return &SyncResult{
		Output:            output,
		Raw:               raw,
		Headers:           headers,
		UpstreamRequestID: upstreamID,
		HTTPStatus:        statusCode,
	}, nil
}

func SubmitAsync(ctx context.Context, m *Manifest, input map[string]any, idempotencyKey string) (*AsyncSubmitResult, *ExecutionError) {
	if m.Submit == nil {
		return nil, &ExecutionError{Code: "invalid_manifest", Message: "async submit is not configured", HTTPStatus: http.StatusInternalServerError}
	}
	spec := httpSpec{
		endpoint:    m.Submit.Endpoint,
		method:      defaultMethod(m.Submit.Method),
		auth:        m.Submit.Auth,
		query:       m.Submit.Query,
		body:        m.Submit.Body,
		defaults:    m.Submit.Defaults,
		consts:      m.Submit.Consts,
		idempotency: idempotencyKey,
	}
	if execErr := validateEnvironment(m); execErr != nil {
		return nil, execErr
	}
	raw, headers, statusCode, upstreamID, execErr := doRequest(ctx, m, spec, input)
	if execErr != nil {
		return nil, execErr
	}
	jobIDValue, ok := Lookup(raw, m.Submit.JobIDPath)
	if !ok {
		return nil, attachResponseMetadata(
			&ExecutionError{Code: "missing_job_id", Message: "upstream did not return a job id", HTTPStatus: http.StatusBadGateway},
			m, statusCode, headers, raw, true, upstreamID,
		)
	}
	jobID, ok := StringValue(jobIDValue)
	if !ok || strings.TrimSpace(jobID) == "" {
		return nil, attachResponseMetadata(
			&ExecutionError{Code: "invalid_job_id", Message: "upstream returned an invalid job id", HTTPStatus: http.StatusBadGateway},
			m, statusCode, headers, raw, true, upstreamID,
		)
	}
	initialStatus := ""
	if m.Poll != nil && m.Poll.StatusPath != "" {
		if v, ok := Lookup(raw, m.Poll.StatusPath); ok {
			initialStatus, _ = StringValue(v)
		}
	}
	return &AsyncSubmitResult{JobID: jobID, InitialStatus: initialStatus, UpstreamRequestID: upstreamID, Raw: raw}, nil
}

func PollAsync(ctx context.Context, m *Manifest, input map[string]any, jobID string) (*AsyncPollResult, *ExecutionError) {
	if m.Poll == nil {
		return nil, &ExecutionError{Code: "invalid_manifest", Message: "async poll is not configured", HTTPStatus: http.StatusInternalServerError}
	}
	endpoint := strings.ReplaceAll(m.Poll.Endpoint, "{job_id}", url.PathEscape(jobID))
	spec := httpSpec{
		endpoint: endpoint,
		method:   defaultMethod(m.Poll.Method),
		auth:     m.Poll.Auth,
		query:    m.Poll.Query,
		body:     m.Poll.Body,
		defaults: m.Poll.Defaults,
		consts:   m.Poll.Consts,
	}
	if execErr := validateEnvironment(m); execErr != nil {
		return nil, execErr
	}
	raw, headers, statusCode, upstreamID, execErr := doRequest(ctx, m, spec, input)
	if execErr != nil {
		return nil, execErr
	}
	st, ok := Lookup(raw, m.Poll.StatusPath)
	if !ok {
		return nil, attachResponseMetadata(
			&ExecutionError{Code: "missing_status", Message: "upstream response did not contain a status", HTTPStatus: http.StatusBadGateway},
			m, statusCode, headers, raw, true, upstreamID,
		)
	}
	status, _ := StringValue(st)
	out := &AsyncPollResult{Status: status, Raw: raw, UpstreamRequestID: upstreamID}
	for _, v := range m.Poll.FailureValues {
		if strings.EqualFold(status, v) {
			out.Completed = true
			out.ErrorCode = "upstream_task_failed"
			out.ErrorMessage = "upstream reported task failure"
			if msg, ok := Lookup(raw, "$.statusMessage"); ok {
				if s, ok := StringValue(msg); ok && s != "" {
					out.ErrorMessage = s
				}
			}
			return out, nil
		}
	}
	for _, v := range m.Poll.SuccessValues {
		if strings.EqualFold(status, v) {
			result, ok := Lookup(raw, m.Poll.ResultPath)
			if !ok {
				return nil, attachResponseMetadata(
					&ExecutionError{Code: "missing_result", Message: "upstream task completed without a result", HTTPStatus: http.StatusBadGateway},
					m, statusCode, headers, raw, true, upstreamID,
				)
			}
			out.Result = result
			out.Completed = true
			return out, nil
		}
	}
	return out, nil
}

// FetchContent downloads the result file for a completed async task.
func FetchContent(ctx context.Context, m *Manifest, input map[string]any, jobID string) ([]byte, string, *ExecutionError) {
	if m.Content == nil || strings.TrimSpace(m.Content.Endpoint) == "" {
		return nil, "", &ExecutionError{
			Code:       "content_not_configured",
			Message:    "this capability does not expose a content endpoint",
			HTTPStatus: http.StatusNotFound,
		}
	}
	endpoint := strings.ReplaceAll(m.Content.Endpoint, "{job_id}", url.PathEscape(jobID))
	timeout, err := m.Timeout()
	if err != nil {
		return nil, "", &ExecutionError{Code: "invalid_timeout", Message: "capability timeout is invalid", HTTPStatus: http.StatusInternalServerError}
	}
	fullURL, err := buildURL(endpoint, mergeQuery(m.Content.Query, m.Content.Consts), nil, input)
	if err != nil {
		return nil, "", &ExecutionError{Code: "invalid_request", Message: "failed to build content URL", HTTPStatus: http.StatusBadRequest}
	}
	if err := validateUpstreamURLContext(ctx, fullURL); err != nil {
		return nil, "", &ExecutionError{Code: "upstream_not_allowed", Message: "content endpoint is not allowed", HTTPStatus: http.StatusBadGateway}
	}
	req, err := http.NewRequestWithContext(ctx, defaultMethod(m.Content.Method), fullURL, nil)
	if err != nil {
		return nil, "", &ExecutionError{Code: "invalid_request", Message: "failed to create content request", HTTPStatus: http.StatusBadRequest}
	}
	req.Header.Set("Accept", "*/*")
	if err := applyAuth(req, m.Content.Auth); err != nil {
		return nil, "", &ExecutionError{Code: "credential_error", Message: "content credential is not configured", HTTPStatus: http.StatusInternalServerError}
	}
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			return validateUpstreamURLContext(next.Context(), next.URL.String())
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", &ExecutionError{Code: "upstream_unreachable", Message: "content request failed", HTTPStatus: http.StatusBadGateway}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", &ExecutionError{Code: "upstream_http_" + strconv.Itoa(resp.StatusCode), Message: "content endpoint returned HTTP " + strconv.Itoa(resp.StatusCode), HTTPStatus: http.StatusBadGateway}
	}
	maxBytes := m.Limits.MaxResponseBytes
	if maxBytes <= 0 {
		maxBytes = 5 * 1024 * 1024
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, "", &ExecutionError{Code: "upstream_read_failed", Message: "failed to read content", HTTPStatus: http.StatusBadGateway}
	}
	if int64(len(raw)) > maxBytes {
		return nil, "", &ExecutionError{Code: "content_too_large", Message: "content exceeded the limit", HTTPStatus: http.StatusBadGateway}
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return raw, contentType, nil
}

func doRequest(ctx context.Context, m *Manifest, spec httpSpec, input map[string]any) (map[string]any, http.Header, int, string, *ExecutionError) {
	timeout, err := m.Timeout()
	if err != nil {
		return nil, nil, 0, "", &ExecutionError{Code: "invalid_timeout", Message: "capability timeout is invalid", HTTPStatus: http.StatusInternalServerError}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	maxBytes := m.Limits.MaxResponseBytes
	if maxBytes <= 0 {
		maxBytes = 1024 * 1024
	}

	query := spec.query
	if !strings.EqualFold(spec.method, http.MethodPost) {
		query = mergeQuery(query, spec.consts)
	}
	endpoint, err := buildURL(spec.endpoint, query, spec.defaults, input)
	if err != nil {
		return nil, nil, 0, "", &ExecutionError{Code: "invalid_request", Message: "failed to build upstream request", HTTPStatus: http.StatusBadRequest}
	}
	if err := validateUpstreamURLContext(ctx, endpoint); err != nil {
		return nil, nil, 0, "", &ExecutionError{Code: "upstream_not_allowed", Message: "upstream endpoint is not allowed", HTTPStatus: http.StatusBadGateway}
	}

	var bodyBytes []byte
	if strings.EqualFold(spec.method, http.MethodPost) &&
		(len(spec.body) > 0 || len(spec.consts) > 0 || len(spec.env) > 0 || spec.bodyWrap == "array") {
		body := make(map[string]any, len(spec.body)+len(spec.consts)+len(spec.env))
		for key, path := range spec.body {
			if v, ok := resolveValue(path, spec.defaults, input, key); ok {
				body[key] = v
			}
		}
		for key, value := range spec.consts {
			body[key] = value
		}
		for key, envName := range spec.env {
			value := strings.TrimSpace(os.Getenv(envName))
			if value == "" {
				return nil, nil, 0, "", &ExecutionError{
					Code:       "credential_error",
					Message:    "upstream credential is not configured",
					HTTPStatus: http.StatusInternalServerError,
				}
			}
			body[key] = value
		}
		var bodyValue any = body
		if spec.bodyWrap == "array" {
			bodyValue = []any{body}
		}
		raw, err := common.Marshal(bodyValue)
		if err != nil {
			return nil, nil, 0, "", &ExecutionError{Code: "invalid_request", Message: "failed to encode upstream request", HTTPStatus: http.StatusBadRequest}
		}
		bodyBytes = raw
	}

	client := &http.Client{
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			return validateUpstreamURLContext(next.Context(), next.URL.String())
		},
	}

	attempts := m.Retry.Attempts
	if attempts < 1 {
		attempts = 1
	}
	var lastErr *ExecutionError
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return nil, nil, 0, "", lastErr
			}
			return nil, nil, 0, "", &ExecutionError{
				Code:       "upstream_timeout",
				Message:    "the overall capability deadline expired before the request was sent",
				HTTPStatus: http.StatusGatewayTimeout,
			}
		}
		var bodyReader io.Reader
		if bodyBytes != nil {
			bodyReader = bytes.NewReader(bodyBytes)
		}
		req, err := http.NewRequestWithContext(ctx, spec.method, endpoint, bodyReader)
		if err != nil {
			return nil, nil, 0, "", &ExecutionError{Code: "invalid_request", Message: "failed to create upstream request", HTTPStatus: http.StatusBadRequest}
		}
		req.Header.Set("Accept", "application/json")
		if bodyReader != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		for name, value := range spec.headers {
			req.Header.Set(name, value)
		}
		if spec.idempotency != "" && m.Idempotency != nil && m.Idempotency.Header != "" {
			req.Header.Set(m.Idempotency.Header, spec.idempotency)
		}
		if err := applyAuth(req, spec.auth); err != nil {
			return nil, nil, 0, "", &ExecutionError{Code: "credential_error", Message: "upstream credential is not configured", HTTPStatus: http.StatusInternalServerError}
		}

		requestWritten := false
		trace := &httptrace.ClientTrace{
			WroteRequest: func(httptrace.WroteRequestInfo) {
				// Any completed write callback means the request may have reached
				// the provider. Retrying after this point can incur duplicate cost.
				requestWritten = true
			},
		}
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
		resp, err := client.Do(req)
		if err != nil {
			if !requestWritten && ctx.Err() == nil && attempt < attempts {
				if !waitForRetry(ctx, time.Duration(attempt)*200*time.Millisecond) {
					break
				}
				continue
			}
			lastErr = transportExecutionError(ctx, requestWritten)
			return nil, nil, 0, "", lastErr
		}
		headers := resp.Header.Clone()
		upstreamID := responseRequestID(headers)
		limited := io.LimitReader(resp.Body, maxBytes+1)
		rawBody, readErr := io.ReadAll(limited)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, nil, resp.StatusCode, "", attachResponseMetadata(
				&ExecutionError{Code: "upstream_read_failed", Message: "failed to read upstream response", HTTPStatus: http.StatusBadGateway},
				m, resp.StatusCode, headers, nil, true, upstreamID,
			)
		}
		if int64(len(rawBody)) > maxBytes {
			return nil, nil, resp.StatusCode, "", attachResponseMetadata(
				&ExecutionError{Code: "upstream_response_too_large", Message: "upstream response exceeded the limit", HTTPStatus: http.StatusBadGateway},
				m, resp.StatusCode, headers, nil, true, upstreamID,
			)
		}
		effectiveStatus := resp.StatusCode
		if m.Response != nil && m.Response.StatusHeader != "" {
			if targetStatus, parseErr := strconv.Atoi(strings.TrimSpace(resp.Header.Get(m.Response.StatusHeader))); parseErr == nil {
				effectiveStatus = targetStatus
			}
		}
		providerError := m.Response != nil && m.Response.ErrorHeader != "" &&
			strings.TrimSpace(resp.Header.Get(m.Response.ErrorHeader)) != ""
		if resp.StatusCode < 200 || resp.StatusCode >= 300 || providerError {
			codeStatus := effectiveStatus
			if codeStatus <= 0 {
				codeStatus = resp.StatusCode
			}
			lastErr = mapUpstreamHTTPError(m.Response, rawBody, codeStatus)
			lastErr.Retryable = containsInt(m.Retry.RetryOn, codeStatus)
			lastErr = attachResponseMetadata(lastErr, m, resp.StatusCode, headers, nil, false, upstreamID)
			if attempt < attempts && lastErr.Retryable && ctx.Err() == nil {
				if !waitForRetry(ctx, time.Duration(attempt)*200*time.Millisecond) {
					break
				}
				continue
			}
			return nil, nil, resp.StatusCode, upstreamID, lastErr
		}
		var parsed map[string]any
		if err := common.Unmarshal(rawBody, &parsed); err != nil {
			return nil, nil, resp.StatusCode, upstreamID, attachResponseMetadata(
				&ExecutionError{Code: "invalid_upstream_json", Message: "upstream returned invalid JSON", HTTPStatus: http.StatusBadGateway},
				m, resp.StatusCode, headers, nil, true, upstreamID,
			)
		}
		if m.Response != nil && m.Response.Success != nil {
			if successErr := validateResponseSuccess(m.Response.Success, parsed); successErr != nil {
				// A 2xx response may already be billed. Never retry after it was
				// received, even when its business status is a known retry value.
				return nil, nil, resp.StatusCode, upstreamID, attachResponseMetadata(
					successErr, m, resp.StatusCode, headers, parsed, true, upstreamID,
				)
			}
		}
		return parsed, headers, resp.StatusCode, upstreamID, nil
	}
	if ctx.Err() != nil && lastErr != nil {
		return nil, nil, 0, "", lastErr
	}
	return nil, nil, 0, "", lastErr
}

func waitForRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func transportExecutionError(ctx context.Context, requestWritten bool) *ExecutionError {
	if requestWritten {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return &ExecutionError{
				Code:             "upstream_timeout",
				Message:          "the upstream result is unknown because the overall deadline expired; the request may have been billed, do not retry immediately",
				HTTPStatus:       http.StatusGatewayTimeout,
				RequestWritten:   true,
				ResponseReceived: false,
				MayHaveCharged:   true,
			}
		}
		return &ExecutionError{
			Code:             "upstream_request_uncertain",
			Message:          "the upstream connection failed after the request was sent; the result is unknown, do not retry immediately",
			HTTPStatus:       http.StatusBadGateway,
			RequestWritten:   true,
			ResponseReceived: false,
			MayHaveCharged:   true,
		}
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &ExecutionError{
			Code:       "upstream_timeout",
			Message:    "the capability deadline expired before the request was written",
			HTTPStatus: http.StatusGatewayTimeout,
		}
	}
	return &ExecutionError{
		Code:       "upstream_unreachable",
		Message:    "upstream request failed before it was sent",
		HTTPStatus: http.StatusBadGateway,
	}
}

func attachResponseMetadata(
	err *ExecutionError,
	m *Manifest,
	status int,
	headers http.Header,
	raw map[string]any,
	mayHaveCharged bool,
	upstreamID string,
) *ExecutionError {
	if err == nil {
		return nil
	}
	err.ResponseReceived = true
	err.RequestWritten = true
	err.MayHaveCharged = mayHaveCharged
	err.UpstreamStatus = status
	err.BillingHeaders = billingHeaders(m, headers)
	err.BillingRaw = raw
	err.UpstreamRequestID = upstreamID
	if status > 0 && err.HTTPStatus == 0 {
		err.HTTPStatus = status
	}
	return err
}

func billingHeaders(m *Manifest, headers http.Header) http.Header {
	if m == nil || headers == nil || strings.TrimSpace(m.Billing.QuantityHeader) == "" {
		return nil
	}
	value := strings.TrimSpace(headers.Get(m.Billing.QuantityHeader))
	if value == "" {
		return nil
	}
	return http.Header{http.CanonicalHeaderKey(m.Billing.QuantityHeader): []string{value}}
}

func responseRequestID(headers http.Header) string {
	if headers == nil {
		return ""
	}
	if id := headers.Get("X-Request-ID"); id != "" {
		return id
	}
	return headers.Get("X-Request-Id")
}

func buildURL(endpoint string, query map[string]string, defaults map[string]any, input map[string]any) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid endpoint")
	}
	q := u.Query()
	for key, path := range query {
		if v, ok := resolveValue(path, defaults, input, key); ok {
			if s, ok := StringValue(v); ok {
				q.Set(key, s)
			}
		}
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func mergeQuery(query map[string]string, consts map[string]string) map[string]string {
	if len(query) == 0 && len(consts) == 0 {
		return nil
	}
	merged := make(map[string]string, len(query)+len(consts))
	for key, value := range query {
		merged[key] = value
	}
	for key, value := range consts {
		merged[key] = value
	}
	return merged
}

func mapUpstreamHTTPError(spec *ResponseSpec, body []byte, status int) *ExecutionError {
	fallback := &ExecutionError{
		Code:       "upstream_http_" + strconv.Itoa(status),
		Message:    "upstream returned HTTP " + strconv.Itoa(status),
		HTTPStatus: http.StatusBadGateway,
		LogCode:    "upstream_http_" + strconv.Itoa(status),
	}
	if spec == nil || len(spec.ErrorMap) == 0 {
		return fallback
	}
	var parsed map[string]any
	if err := common.Unmarshal(body, &parsed); err == nil && parsed != nil {
		if strings.TrimSpace(spec.ErrorCodePath) != "" {
			if value, ok := Lookup(parsed, spec.ErrorCodePath); ok {
				if code, ok := StringValue(value); ok {
					if mapping, ok := spec.ErrorMap[code]; ok {
						return executionErrorFromMapping(mapping)
					}
				}
			}
		}
	}
	if mapping, ok := spec.ErrorMap[strconv.Itoa(status)]; ok {
		return executionErrorFromMapping(mapping)
	}
	return fallback
}

func executionErrorFromMapping(mapping ErrorMapSpec) *ExecutionError {
	logCode := strings.TrimSpace(mapping.LogCode)
	if logCode == "" {
		logCode = "upstream_error_mapped"
	}
	return &ExecutionError{
		Code:       mapping.Code,
		Message:    mapping.Message,
		HTTPStatus: mapping.HTTPStatus,
		LogCode:    logCode,
	}
}

func resolveValue(path string, defaults map[string]any, input map[string]any, field string) (any, bool) {
	if v, ok := Lookup(map[string]any{"input": input}, path); ok {
		return v, true
	}
	if defaults != nil {
		if v, ok := defaults[field]; ok {
			return v, true
		}
	}
	return nil, false
}

func isEmptyInputValue(value any) bool {
	if value == nil {
		return true
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text) == ""
	}
	switch typed := value.(type) {
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	}
	return false
}

func validateURLInputValue(value any, allowedHosts []string) error {
	raw, ok := value.(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return fmt.Errorf("URL input must be a non-empty string")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Opaque != "" {
		return fmt.Errorf("invalid URL")
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return fmt.Errorf("only https is allowed")
	}
	if parsed.User != nil {
		return fmt.Errorf("userinfo is not allowed")
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "" || net.ParseIP(host) != nil {
		return fmt.Errorf("IP literals and empty hosts are not allowed")
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return fmt.Errorf("non-default ports are not allowed")
	}
	for _, allowed := range allowedHosts {
		allowed = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(allowed)), ".")
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return nil
		}
	}
	return fmt.Errorf("host is not allowed")
}

func validateEnvironment(m *Manifest) *ExecutionError {
	credentialError := func() *ExecutionError {
		return &ExecutionError{
			Code:       "credential_error",
			Message:    "upstream credential is not configured",
			HTTPStatus: http.StatusInternalServerError,
		}
	}
	checkAuth := func(auth *AuthSpec) *ExecutionError {
		if auth == nil || strings.TrimSpace(auth.Type) == "" {
			return nil
		}
		if strings.TrimSpace(auth.SecretRef) == "" || strings.TrimSpace(os.Getenv(auth.SecretRef)) == "" {
			return credentialError()
		}
		return nil
	}
	authSpecs := []*AuthSpec{m.Auth}
	if m.Submit != nil {
		authSpecs = append(authSpecs, m.Submit.Auth)
	}
	if m.Poll != nil {
		authSpecs = append(authSpecs, m.Poll.Auth)
	}
	if m.Content != nil {
		authSpecs = append(authSpecs, m.Content.Auth)
	}
	for _, auth := range authSpecs {
		if err := checkAuth(auth); err != nil {
			return err
		}
	}
	if m.Request != nil {
		for _, envName := range m.Request.Env {
			if strings.TrimSpace(envName) == "" || strings.TrimSpace(os.Getenv(envName)) == "" {
				return credentialError()
			}
		}
	}
	return nil
}

func validateResponseSuccess(spec *SuccessSpec, raw map[string]any) *ExecutionError {
	if spec == nil {
		return nil
	}
	if spec.Path != "" {
		value, ok := Lookup(raw, spec.Path)
		matched := false
		if ok {
			for _, expected := range spec.Values {
				if responseValuesEqual(value, expected) {
					matched = true
					break
				}
			}
		}
		if !matched {
			code := strings.TrimSpace(spec.FailureCode)
			if code == "" {
				code = "upstream_target_status"
			}
			return &ExecutionError{
				Code:       code,
				Message:    "upstream target status is not accepted",
				HTTPStatus: http.StatusBadGateway,
			}
		}
	}
	if spec.NonEmpty != "" {
		value, ok := Lookup(raw, spec.NonEmpty)
		if !ok || !isNonEmptyResponseValue(value) {
			code := strings.TrimSpace(spec.EmptyCode)
			if code == "" {
				code = "upstream_empty_body"
			}
			return &ExecutionError{
				Code:       code,
				Message:    "upstream response body is empty",
				HTTPStatus: http.StatusBadGateway,
			}
		}
	}
	return nil
}

func responseValuesEqual(actual any, expected any) bool {
	if actualNumber, ok := NumberValue(actual); ok {
		if expectedNumber, ok := NumberValue(expected); ok {
			return actualNumber == expectedNumber
		}
	}
	actualString, actualOK := StringValue(actual)
	expectedString, expectedOK := StringValue(expected)
	return actualOK && expectedOK && actualString == expectedString
}

func isNonEmptyResponseValue(value any) bool {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) != ""
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	case nil:
		return false
	default:
		return true
	}
}

func successFailureCode(spec *SuccessSpec) string {
	if spec == nil || strings.TrimSpace(spec.FailureCode) == "" {
		return "upstream_target_status"
	}
	return strings.TrimSpace(spec.FailureCode)
}

func statusFromResponse(spec *SuccessSpec, raw map[string]any) int {
	if spec == nil || spec.Path == "" {
		return 0
	}
	value, ok := Lookup(raw, spec.Path)
	if !ok {
		return 0
	}
	status, ok := NumberValue(value)
	if !ok {
		return 0
	}
	return int(status)
}

func applyAuth(req *http.Request, auth *AuthSpec) error {
	if auth == nil || auth.Type == "" {
		return nil
	}
	secret := strings.TrimSpace(os.Getenv(auth.SecretRef))
	if secret == "" {
		return fmt.Errorf("missing secret %s", auth.SecretRef)
	}
	switch auth.Type {
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+secret)
	case "header":
		if auth.Header == "" {
			return fmt.Errorf("header auth requires header")
		}
		req.Header.Set(auth.Header, secret)
	case "query":
		if auth.Query == "" {
			return fmt.Errorf("query auth requires query")
		}
		q := req.URL.Query()
		q.Set(auth.Query, secret)
		req.URL.RawQuery = q.Encode()
	default:
		return fmt.Errorf("unsupported auth type %s", auth.Type)
	}
	return nil
}

func validateUpstreamURL(raw string) error {
	return validateUpstreamURLContext(context.Background(), raw)
}

func validateUpstreamURLContext(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("invalid url")
	}
	allowHTTP := os.Getenv("CAPABILITY_ALLOW_INSECURE_URLS") == "1"
	if u.Scheme != "https" && !(allowHTTP && u.Scheme == "http") {
		return fmt.Errorf("only https is allowed")
	}
	allowPrivate := os.Getenv("CAPABILITY_ALLOW_PRIVATE_URLS") == "1"
	if allowPrivate {
		return nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
	if err != nil || len(addrs) == 0 {
		return fmt.Errorf("cannot resolve host")
	}
	for _, addr := range addrs {
		ip := addr.IP
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return fmt.Errorf("private address is not allowed")
		}
	}
	return nil
}

func defaultMethod(method string) string {
	if strings.TrimSpace(method) == "" {
		return http.MethodGet
	}
	return strings.ToUpper(strings.TrimSpace(method))
}

func containsInt(values []int, target int) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}
