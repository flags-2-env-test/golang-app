package middlewareconsumer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	oresmiddleware "github.com/ORESoftware/ores-middleware/src/golang"
	"github.com/ORESoftware/ores-middleware/src/golang/adapters"
)

type headerAuth struct{}

func (headerAuth) Verify(_ context.Context, request *http.Request, _ oresmiddleware.RequestContext) (oresmiddleware.AuthDecision, error) {
	return oresmiddleware.AuthDecision{
		UserID:   request.Header.Get("X-User"),
		TenantID: request.Header.Get("X-Tenant"),
	}, nil
}

type denyLimiter struct{}

func (denyLimiter) Allow(context.Context, string, int, float64) (bool, error) {
	return false, nil
}

func testConfig(service string) oresmiddleware.Config {
	cfg := oresmiddleware.DefaultConfig(service)
	cfg.Environment = oresmiddleware.Test
	cfg.Settings.TLS.RequireHTTPS = false
	cfg.Settings.TLS.Mode = "disabled"
	cfg.Settings.RateLimit.Enabled = false
	cfg.Settings.Compression.Enabled = false
	cfg.Settings.Idempotency.Enabled = false
	cfg.Integrations.SharedAuth.Mode = oresmiddleware.IntegrationDisabled
	cfg.Integrations.OptoSync.Mode = oresmiddleware.IntegrationDisabled
	return cfg
}

func newStack(t *testing.T, cfg oresmiddleware.Config, deps oresmiddleware.Dependencies) *oresmiddleware.Stack {
	t.Helper()
	stack, err := oresmiddleware.New(cfg, deps)
	if err != nil {
		t.Fatalf("new middleware stack: %v", err)
	}
	return stack
}

func TestContractVersionAndProductionSafety(t *testing.T) {
	if oresmiddleware.ContractVersion != "1.0.0" {
		t.Fatalf("contract version = %q", oresmiddleware.ContractVersion)
	}
	cfg := oresmiddleware.DefaultConfig("test-org-production-negative-control")
	cfg.Environment = oresmiddleware.Production
	cfg.Settings.FaultInjection.Enabled = true
	cfg.Settings.TestAuthBypass.Enabled = true
	issues := oresmiddleware.ValidateConfig(cfg)
	count := 0
	for _, issue := range issues {
		if issue.Code == "production_forbidden" {
			count++
		}
	}
	if count < 2 {
		t.Fatalf("expected both production safety controls to fail, got %#v", issues)
	}
}

func TestConcurrentRequestContextIsolation(t *testing.T) {
	cfg := testConfig("flags-2-env-test-go")
	stack := newStack(t, cfg, oresmiddleware.Dependencies{AuthVerifier: headerAuth{}})

	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ctx, ok := oresmiddleware.CurrentContext(request.Context())
		if !ok {
			t.Errorf("missing request context")
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		expectedRequestID := request.Header.Get("X-Request-ID")
		if ctx.RequestID != expectedRequestID {
			t.Errorf("request id = %q, want %q", ctx.RequestID, expectedRequestID)
		}
		if ctx.UserID != request.Header.Get("X-User") {
			t.Errorf("user id = %q", ctx.UserID)
		}
		if ctx.TenantID != request.Header.Get("X-Tenant") {
			t.Errorf("tenant id = %q", ctx.TenantID)
		}
		time.Sleep(time.Duration(len(expectedRequestID)%5) * time.Millisecond)
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]string{"requestId": ctx.RequestID})
	})

	server := httptest.NewServer(adapters.NetHTTP(stack, handler))
	defer server.Close()

	client := server.Client()
	const requests = 64
	var wg sync.WaitGroup
	wg.Add(requests)
	for index := 0; index < requests; index++ {
		index := index
		go func() {
			defer wg.Done()
			requestID := "go-request-" + strconv.Itoa(index)
			req, err := http.NewRequest(http.MethodGet, server.URL+"/concurrent/"+strconv.Itoa(index), nil)
			if err != nil {
				t.Errorf("new request: %v", err)
				return
			}
			req.Header.Set("Accept", "application/json")
			req.Header.Set("X-Request-ID", requestID)
			req.Header.Set("X-User", "user-"+strconv.Itoa(index))
			req.Header.Set("X-Tenant", "tenant-"+strconv.Itoa(index%7))
			resp, err := client.Do(req)
			if err != nil {
				t.Errorf("request %d: %v", index, err)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("request %d status = %d", index, resp.StatusCode)
				return
			}
			if got := resp.Header.Get("X-Request-ID"); got != requestID {
				t.Errorf("request %d response request id = %q", index, got)
			}
		}()
	}
	wg.Wait()
}

func TestMalformedRequestIDIsNotReflected(t *testing.T) {
	stack := newStack(t, testConfig("go-request-id"), oresmiddleware.Dependencies{})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://example.test/request-id", nil)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Request-ID", "bad request id")
	stack.Wrap(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"ok":true}`))
	})).ServeHTTP(recorder, request)

	got := recorder.Header().Get("X-Request-ID")
	if got == "" || got == "bad request id" {
		t.Fatalf("unsafe request id reflected: %q", got)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9._-]+$`).MatchString(got) {
		t.Fatalf("generated request id is not token-safe: %q", got)
	}
}

func TestDeclaredOversizedBodyStopsDispatch(t *testing.T) {
	cfg := testConfig("go-body-limit")
	cfg.Settings.MaxBodyBytes = 4
	stack := newStack(t, cfg, oresmiddleware.Dependencies{})
	var called atomic.Bool
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "http://example.test/body", strings.NewReader("12345"))
	request.ContentLength = 5
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	stack.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called.Store(true) })).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", recorder.Code)
	}
	if called.Load() {
		t.Fatal("downstream handler ran for oversized body")
	}
}

func TestUntrustedForwardedTransportIsRejected(t *testing.T) {
	cfg := testConfig("go-forwarded-header")
	cfg.Settings.TLS.StrictForwardedHeaders = true
	stack := newStack(t, cfg, oresmiddleware.Dependencies{})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://example.test/proxy", nil)
	request.RemoteAddr = "198.51.100.10:1234"
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Forwarded-Proto", "https")
	stack.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("downstream handler must not run")
	})).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestRateLimitDenialIsFailClosed(t *testing.T) {
	cfg := testConfig("go-rate-limit")
	cfg.Settings.RateLimit.Enabled = true
	stack := newStack(t, cfg, oresmiddleware.Dependencies{RateLimiter: denyLimiter{}})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://example.test/limited", nil)
	request.Header.Set("Accept", "application/json")
	stack.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("downstream handler must not run")
	})).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestSharedAuthWithoutIdentityIsRejected(t *testing.T) {
	cfg := testConfig("go-shared-auth")
	cfg.Integrations.SharedAuth.Mode = oresmiddleware.IntegrationEmbedded
	stack := newStack(t, cfg, oresmiddleware.Dependencies{})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://example.test/protected", nil)
	request.Header.Set("Accept", "application/json")
	stack.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("downstream handler must not run")
	})).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestDeadlineStopsSlowHandler(t *testing.T) {
	cfg := testConfig("go-deadline")
	cfg.Settings.TimeoutMS = 5
	stack := newStack(t, cfg, oresmiddleware.Dependencies{})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://example.test/slow", nil)
	request.Header.Set("Accept", "application/json")
	stack.Wrap(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		time.Sleep(40 * time.Millisecond)
		_, _ = writer.Write([]byte("late"))
	})).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestIdempotentReplayInvokesHandlerOnce(t *testing.T) {
	cfg := testConfig("go-idempotency")
	cfg.Settings.Idempotency.Enabled = true
	stack := newStack(t, cfg, oresmiddleware.Dependencies{})
	var calls atomic.Int32
	handler := stack.Wrap(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		value := calls.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"calls":` + strconv.Itoa(int(value)) + `}`))
	}))

	invoke := func() *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "http://example.test/replay", strings.NewReader("{}"))
		request.Header.Set("Accept", "application/json")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "go-replay-key")
		handler.ServeHTTP(recorder, request)
		return recorder
	}

	first := invoke()
	second := invoke()
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("statuses = %d, %d", first.Code, second.Code)
	}
	if calls.Load() != 1 {
		t.Fatalf("handler calls = %d", calls.Load())
	}
	firstBody, _ := io.ReadAll(first.Result().Body)
	secondBody, _ := io.ReadAll(second.Result().Body)
	if string(firstBody) != string(secondBody) || !strings.Contains(string(secondBody), `"calls":1`) {
		t.Fatalf("replay mismatch: first=%s second=%s", firstBody, secondBody)
	}
}
