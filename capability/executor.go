package capability

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

type ExecutionError struct {
	Code       string
	Message    string
	HTTPStatus int
	Retryable  bool
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
	defaults    map[string]any
	consts      map[string]string
	env         map[string]string
	idempotency string
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
		spec.defaults = m.Request.Defaults
		spec.consts = m.Request.Consts
		spec.env = m.Request.Env
	}
	raw, status, upstreamID, execErr := doRequest(ctx, m, spec, input)
	if execErr != nil {
		return nil, execErr
	}
	output := make(map[string]any, len(m.Response.Output))
	for key, path := range m.Response.Output {
		value, ok := Lookup(raw, path)
		if !ok {
			return nil, &ExecutionError{
				Code:       "missing_output_field",
				Message:    "upstream response did not contain a required field",
				HTTPStatus: http.StatusBadGateway,
			}
		}
		output[key] = value
	}
	return &SyncResult{
		Output:            output,
		Raw:               raw,
		UpstreamRequestID: upstreamID,
		HTTPStatus:        status,
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
	raw, _, upstreamID, execErr := doRequest(ctx, m, spec, input)
	if execErr != nil {
		return nil, execErr
	}
	jobIDValue, ok := Lookup(raw, m.Submit.JobIDPath)
	if !ok {
		return nil, &ExecutionError{Code: "missing_job_id", Message: "upstream did not return a job id", HTTPStatus: http.StatusBadGateway}
	}
	jobID, ok := StringValue(jobIDValue)
	if !ok || strings.TrimSpace(jobID) == "" {
		return nil, &ExecutionError{Code: "invalid_job_id", Message: "upstream returned an invalid job id", HTTPStatus: http.StatusBadGateway}
	}
	status := ""
	if m.Poll != nil && m.Poll.StatusPath != "" {
		if v, ok := Lookup(raw, m.Poll.StatusPath); ok {
			status, _ = StringValue(v)
		}
	}
	return &AsyncSubmitResult{JobID: jobID, InitialStatus: status, UpstreamRequestID: upstreamID, Raw: raw}, nil
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
	raw, _, upstreamID, execErr := doRequest(ctx, m, spec, input)
	if execErr != nil {
		return nil, execErr
	}
	st, ok := Lookup(raw, m.Poll.StatusPath)
	if !ok {
		return nil, &ExecutionError{Code: "missing_status", Message: "upstream response did not contain a status", HTTPStatus: http.StatusBadGateway}
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
				return nil, &ExecutionError{Code: "missing_result", Message: "upstream task completed without a result", HTTPStatus: http.StatusBadGateway}
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
	fullURL, err := buildURL(endpoint, m.Content.Query, m.Content.Consts, nil, input)
	if err != nil {
		return nil, "", &ExecutionError{Code: "invalid_request", Message: "failed to build content URL", HTTPStatus: http.StatusBadRequest}
	}
	if err := validateUpstreamURL(fullURL); err != nil {
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
			return validateUpstreamURL(next.URL.String())
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

func doRequest(ctx context.Context, m *Manifest, spec httpSpec, input map[string]any) (map[string]any, int, string, *ExecutionError) {
	timeout, err := m.Timeout()
	if err != nil {
		return nil, 0, "", &ExecutionError{Code: "invalid_timeout", Message: "capability timeout is invalid", HTTPStatus: http.StatusInternalServerError}
	}
	maxBytes := m.Limits.MaxResponseBytes
	if maxBytes <= 0 {
		maxBytes = 1024 * 1024
	}

	endpoint, err := buildURL(spec.endpoint, spec.query, spec.consts, spec.defaults, input)
	if err != nil {
		return nil, 0, "", &ExecutionError{Code: "invalid_request", Message: "failed to build upstream request", HTTPStatus: http.StatusBadRequest}
	}
	if err := validateUpstreamURL(endpoint); err != nil {
		return nil, 0, "", &ExecutionError{Code: "upstream_not_allowed", Message: "upstream endpoint is not allowed", HTTPStatus: http.StatusBadGateway}
	}

	var bodyBytes []byte
	if strings.EqualFold(spec.method, http.MethodPost) &&
		(len(spec.body) > 0 || len(spec.consts) > 0 || len(spec.env) > 0) {
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
				return nil, 0, "", &ExecutionError{
					Code:       "credential_error",
					Message:    "upstream credential is not configured",
					HTTPStatus: http.StatusInternalServerError,
				}
			}
			body[key] = value
		}
		raw, err := common.Marshal(body)
		if err != nil {
			return nil, 0, "", &ExecutionError{Code: "invalid_request", Message: "failed to encode upstream request", HTTPStatus: http.StatusBadRequest}
		}
		bodyBytes = raw
	}

	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			return validateUpstreamURL(next.URL.String())
		},
	}

	attempts := m.Retry.Attempts
	if attempts < 1 {
		attempts = 1
	}
	var lastErr *ExecutionError
	for attempt := 1; attempt <= attempts; attempt++ {
		var bodyReader io.Reader
		if bodyBytes != nil {
			bodyReader = bytes.NewReader(bodyBytes)
		}
		req, err := http.NewRequestWithContext(ctx, spec.method, endpoint, bodyReader)
		if err != nil {
			return nil, 0, "", &ExecutionError{Code: "invalid_request", Message: "failed to create upstream request", HTTPStatus: http.StatusBadRequest}
		}
		req.Header.Set("Accept", "application/json")
		if bodyReader != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if spec.idempotency != "" && m.Idempotency != nil && m.Idempotency.Header != "" {
			req.Header.Set(m.Idempotency.Header, spec.idempotency)
		}
		if err := applyAuth(req, spec.auth); err != nil {
			return nil, 0, "", &ExecutionError{Code: "credential_error", Message: "upstream credential is not configured", HTTPStatus: http.StatusInternalServerError}
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = &ExecutionError{Code: "upstream_unreachable", Message: "upstream request failed", HTTPStatus: http.StatusBadGateway}
			if attempt < attempts {
				time.Sleep(time.Duration(attempt) * 200 * time.Millisecond)
				continue
			}
			return nil, 0, "", lastErr
		}
		limited := io.LimitReader(resp.Body, maxBytes+1)
		rawBody, readErr := io.ReadAll(limited)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, 0, "", &ExecutionError{Code: "upstream_read_failed", Message: "failed to read upstream response", HTTPStatus: http.StatusBadGateway}
		}
		if int64(len(rawBody)) > maxBytes {
			return nil, 0, "", &ExecutionError{Code: "upstream_response_too_large", Message: "upstream response exceeded the limit", HTTPStatus: http.StatusBadGateway}
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
			lastErr = &ExecutionError{
				Code:       "upstream_http_" + strconv.Itoa(codeStatus),
				Message:    "upstream returned HTTP " + strconv.Itoa(codeStatus),
				HTTPStatus: http.StatusBadGateway,
				Retryable:  containsInt(m.Retry.RetryOn, codeStatus),
			}
			if attempt < attempts && lastErr.Retryable {
				time.Sleep(time.Duration(attempt) * 200 * time.Millisecond)
				continue
			}
			return nil, resp.StatusCode, "", lastErr
		}
		var parsed map[string]any
		if err := common.Unmarshal(rawBody, &parsed); err != nil {
			return nil, resp.StatusCode, "", &ExecutionError{Code: "invalid_upstream_json", Message: "upstream returned invalid JSON", HTTPStatus: http.StatusBadGateway}
		}
		if m.Response != nil && m.Response.Success != nil {
			if successErr := validateResponseSuccess(m.Response.Success, parsed); successErr != nil {
				successErr.Retryable = successErr.Code == successFailureCode(m.Response.Success) &&
					containsInt(m.Retry.RetryOn, statusFromResponse(m.Response.Success, parsed))
				if attempt < attempts && successErr.Retryable {
					time.Sleep(time.Duration(attempt) * 200 * time.Millisecond)
					continue
				}
				return nil, resp.StatusCode, "", successErr
			}
		}
		upstreamID := resp.Header.Get("X-Request-Id")
		if upstreamID == "" {
			upstreamID = resp.Header.Get("X-Request-ID")
		}
		return parsed, resp.StatusCode, upstreamID, nil
	}
	return nil, 0, "", lastErr
}

func buildURL(endpoint string, query map[string]string, consts map[string]string, defaults map[string]any, input map[string]any) (string, error) {
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
	for key, value := range consts {
		q.Set(key, value)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
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
	ips, err := net.LookupIP(u.Hostname())
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("cannot resolve host")
	}
	for _, ip := range ips {
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
