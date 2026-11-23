package nextdns

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/matryer/is"
)

// scriptedServer answers each request with the next response in the script
// (the last one repeats) and records what it received.
type scriptedServer struct {
	mu       sync.Mutex
	script   []scriptedResponse
	requests []receivedRequest
	*httptest.Server
}

type scriptedResponse struct {
	status     int
	body       string
	retryAfter string
}

type receivedRequest struct {
	method string
	body   string
	keys   []string // X-Api-Key values
}

func newScriptedServer(t *testing.T, script ...scriptedResponse) *scriptedServer {
	t.Helper()
	s := &scriptedServer{script: script}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		n := len(s.requests)
		s.requests = append(s.requests, receivedRequest{method: r.Method, body: string(body), keys: r.Header.Values("X-Api-Key")})
		resp := s.script[min(n, len(s.script)-1)]
		s.mu.Unlock()
		if resp.retryAfter != "" {
			w.Header().Set("Retry-After", resp.retryAfter)
		}
		w.WriteHeader(resp.status)
		_, _ = w.Write([]byte(resp.body))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *scriptedServer) received() []receivedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]receivedRequest(nil), s.requests...)
}

// fastClient returns a client for the scripted server with the retry waits
// shortened so the tests run quickly.
func fastClient(t *testing.T, s *scriptedServer) *Client {
	t.Helper()
	c, err := New(WithBaseURL(s.URL), WithAPIKey("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	c.firstRetryWait = time.Millisecond
	c.retryWaitCap = 50 * time.Millisecond
	return c
}

const okSecurity = `{"data":{"threatIntelligenceFeeds":true}}`

// A 5xx used to panic in (*Error).Error, which dereferenced the nil error body.
func TestErrorStringWithoutErrorBody(t *testing.T) {
	c := is.New(t)
	for _, e := range []*Error{
		{Type: ErrorTypeServiceError, Message: errInternalServiceError, StatusCode: http.StatusBadGateway},
		{Type: ErrorTypeMalformed, Message: errMalformedErrorBody},
		{Type: ErrorTypeRequest, Message: errResponseError, Errors: &ErrorResponse{}},
	} {
		c.True(strings.HasPrefix(e.Error(), e.Message))
	}
	c.Equal((&Error{Message: errInternalServiceError, StatusCode: 502}).Error(), "internal service error received (HTTP 502 Bad Gateway)")
}

func TestServerErrorIsRetriedThenSucceeds(t *testing.T) {
	c := is.New(t)
	s := newScriptedServer(t,
		scriptedResponse{status: http.StatusServiceUnavailable, body: "<html>unavailable</html>"},
		scriptedResponse{status: http.StatusBadGateway, body: "bad gateway"},
		scriptedResponse{status: http.StatusOK, body: okSecurity},
	)
	client := fastClient(t, s)

	sec, err := client.Security.Get(context.Background(), &GetSecurityRequest{ProfileID: "abc123"})
	c.NoErr(err)
	c.True(sec.ThreatIntelligenceFeeds)
	c.Equal(len(s.received()), 3)
}

func TestServerErrorGivesUpAfterFourAttemptsWithoutPanic(t *testing.T) {
	c := is.New(t)
	s := newScriptedServer(t, scriptedResponse{status: http.StatusInternalServerError, body: "oops, not JSON"})
	client := fastClient(t, s)

	_, err := client.Security.Get(context.Background(), &GetSecurityRequest{ProfileID: "abc123"})
	c.True(err != nil)
	c.True(strings.Contains(err.Error(), "internal service error received (HTTP 500"))
	var apiErr *Error
	c.True(errors.As(err, &apiErr))
	c.Equal(apiErr.Type, ErrorTypeServiceError)
	c.Equal(len(s.received()), 4)
}

// Every idempotent method is retried on a 5xx, and the request body is sent
// again in full on each attempt, with the API key exactly once.
func TestIdempotentWritesAreRetriedWithTheirBody(t *testing.T) {
	c := is.New(t)
	s := newScriptedServer(t,
		scriptedResponse{status: http.StatusBadGateway},
		scriptedResponse{status: http.StatusNoContent},
	)
	client := fastClient(t, s)

	err := client.SecurityTlds.Create(context.Background(), &CreateSecurityTldsRequest{
		ProfileID: "abc123", SecurityTlds: []*SecurityTlds{{ID: "example"}},
	})
	c.NoErr(err)
	got := s.received()
	c.Equal(len(got), 2)
	for _, r := range got {
		c.Equal(r.method, http.MethodPut)
		c.Equal(strings.TrimSpace(r.body), `[{"id":"example"}]`)
		c.Equal(r.keys, []string{"test-key"})
	}

	for method, call := range map[string]func(*Client) error{
		http.MethodPatch: func(client *Client) error {
			return client.Security.Update(context.Background(), &UpdateSecurityRequest{ProfileID: "abc123", Security: &Security{}})
		},
		http.MethodDelete: func(client *Client) error {
			return client.Rewrites.Delete(context.Background(), &DeleteRewritesRequest{ProfileID: "abc123", ID: "rw01"})
		},
	} {
		s := newScriptedServer(t, scriptedResponse{status: http.StatusServiceUnavailable}, scriptedResponse{status: http.StatusNoContent})
		c.NoErr(call(fastClient(t, s)))
		got := s.received()
		c.Equal(len(got), 2)
		c.Equal(got[1].method, method)
		c.Equal(got[0].body, got[1].body)
	}
}

// A POST that fails with a 5xx may have been applied, so it is not retried.
func TestPostIsNotRetriedOnServerError(t *testing.T) {
	c := is.New(t)
	s := newScriptedServer(t, scriptedResponse{status: http.StatusInternalServerError})
	client := fastClient(t, s)

	_, err := client.Rewrites.Create(context.Background(), &CreateRewritesRequest{
		ProfileID: "abc123", Rewrites: &Rewrites{Name: "a.example", Content: "192.0.2.1"},
	})
	c.True(err != nil)
	c.Equal(len(s.received()), 1)
}

// A 429 means the request was refused before it was processed, so even a
// POST is retried.
func TestTooManyRequestsIsRetriedForPost(t *testing.T) {
	c := is.New(t)
	s := newScriptedServer(t,
		scriptedResponse{status: http.StatusTooManyRequests, body: "slow down"},
		scriptedResponse{status: http.StatusOK, body: `{"data":{"id":"rw01","name":"a.example","type":"A","content":"192.0.2.1"}}`},
	)
	client := fastClient(t, s)

	rw, err := client.Rewrites.Create(context.Background(), &CreateRewritesRequest{
		ProfileID: "abc123", Rewrites: &Rewrites{Name: "a.example", Content: "192.0.2.1"},
	})
	c.NoErr(err)
	c.Equal(rw, "rw01")
	got := s.received()
	c.Equal(len(got), 2)
	c.Equal(got[0].body, got[1].body)
}

func TestTooManyRequestsHonoursRetryAfter(t *testing.T) {
	c := is.New(t)
	s := newScriptedServer(t,
		scriptedResponse{status: http.StatusTooManyRequests, retryAfter: "1"},
		scriptedResponse{status: http.StatusOK, body: okSecurity},
	)
	client := fastClient(t, s)
	client.retryWaitCap = 5 * time.Second

	start := time.Now()
	_, err := client.Security.Get(context.Background(), &GetSecurityRequest{ProfileID: "abc123"})
	c.NoErr(err)
	c.True(time.Since(start) >= time.Second)
	c.Equal(len(s.received()), 2)
}

// A Retry-After longer than the client will wait ends the retries rather
// than retrying early.
func TestRetryAfterBeyondTheCapIsNotRetried(t *testing.T) {
	c := is.New(t)
	s := newScriptedServer(t, scriptedResponse{status: http.StatusTooManyRequests, retryAfter: "3600", body: "<html>rate limited</html>"})
	client := fastClient(t, s)

	_, err := client.Security.Get(context.Background(), &GetSecurityRequest{ProfileID: "abc123"})
	c.True(err != nil)
	c.True(strings.Contains(err.Error(), "malformed error response body received"))
	c.Equal(len(s.received()), 1)
}

func TestClientErrorsAreNotRetried(t *testing.T) {
	c := is.New(t)
	s := newScriptedServer(t, scriptedResponse{status: http.StatusNotFound, body: `{"errors":[{"code":"notFound"}]}`})
	client := fastClient(t, s)

	_, err := client.Security.Get(context.Background(), &GetSecurityRequest{ProfileID: "abc123"})
	c.True(err != nil)
	var apiErr *Error
	c.True(errors.As(err, &apiErr))
	c.Equal(apiErr.Type, ErrorTypeNotFound)
	c.Equal(len(s.received()), 1)
}

// A body that is not JSON, on an error or a success status, is reported as
// an error and does not panic.
func TestNonJSONBodiesAreErrorsNotPanics(t *testing.T) {
	c := is.New(t)
	for _, resp := range []scriptedResponse{
		{status: http.StatusBadRequest, body: "<html>bad request</html>"},
		{status: http.StatusOK, body: "<html>maintenance</html>"},
	} {
		s := newScriptedServer(t, resp)
		client := fastClient(t, s)
		_, err := client.Security.Get(context.Background(), &GetSecurityRequest{ProfileID: "abc123"})
		c.True(err != nil)
		var apiErr *Error
		c.True(errors.As(err, &apiErr))
		c.Equal(apiErr.Type, ErrorTypeMalformed)
		c.True(apiErr.Error() != "")
	}
}

func TestRetryWaitStopsWhenTheContextIsCancelled(t *testing.T) {
	c := is.New(t)
	s := newScriptedServer(t, scriptedResponse{status: http.StatusServiceUnavailable})
	client := fastClient(t, s)
	client.firstRetryWait = time.Hour
	client.retryWaitCap = time.Hour

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := client.Security.Get(ctx, &GetSecurityRequest{ProfileID: "abc123"})
	c.True(errors.Is(err, context.DeadlineExceeded))
	c.Equal(len(s.received()), 1)
}

func TestParseRetryAfter(t *testing.T) {
	c := is.New(t)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, tc := range []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"", 0, false},
		{"soon", 0, false},
		{"0", 0, true},
		{"7", 7 * time.Second, true},
		{"-3", 0, true},
		{"99999999999999", 24 * time.Hour, true},
		{now.Add(30 * time.Second).Format(http.TimeFormat), 30 * time.Second, true},
		{now.Add(-time.Minute).Format(http.TimeFormat), 0, true},
	} {
		got, ok := parseRetryAfter(tc.in, now)
		c.Equal(ok, tc.ok)
		c.Equal(got, tc.want)
	}
}

func TestBackoffGrowsAndIsCapped(t *testing.T) {
	c := is.New(t)
	client := &Client{firstRetryWait: time.Second, retryWaitCap: 5 * time.Second}
	for attempt, want := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 4: 5 * time.Second, 64: 5 * time.Second} {
		for range 20 {
			got := client.backoff(attempt)
			c.True(got >= want/2)
			c.True(got <= want)
		}
	}
}

// The API key is added to every request, so a redirect must never be
// followed: the target would receive the key.
func TestRedirectsAreRefused(t *testing.T) {
	c := is.New(t)
	target := newScriptedServer(t, scriptedResponse{status: http.StatusOK, body: okSecurity})
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/profiles/abc123/security", http.StatusFound)
	}))
	t.Cleanup(origin.Close)

	client, err := New(WithBaseURL(origin.URL), WithAPIKey("test-key"))
	c.NoErr(err)
	_, err = client.Security.Get(context.Background(), &GetSecurityRequest{ProfileID: "abc123"})
	c.True(errors.Is(err, ErrRedirectRefused))
	c.Equal(len(target.received()), 0)

	// A caller-supplied HTTP client is made to refuse redirects as well.
	client, err = New(WithHTTPClient(&http.Client{Transport: http.DefaultTransport}), WithBaseURL(origin.URL), WithAPIKey("test-key"))
	c.NoErr(err)
	_, err = client.Security.Get(context.Background(), &GetSecurityRequest{ProfileID: "abc123"})
	c.True(errors.Is(err, ErrRedirectRefused))
	c.Equal(len(target.received()), 0)
}

func TestValidateBaseURL(t *testing.T) {
	c := is.New(t)
	for _, ok := range []string{
		"https://api.nextdns.io/",
		"https://api.nextdns.io",
		"http://127.0.0.1:8080/",
		"http://127.0.0.1",
		"http://localhost:1234/",
		"http://[::1]:9999/",
		"https://localhost/",
	} {
		_, err := ValidateBaseURL(ok)
		c.NoErr(err)
	}
	for _, bad := range []string{
		"http://api.nextdns.io/",
		"https://api.nextdns.io/other/",
		"https://api.nextdns.io:8443/",
		"https://api.nextdns.io.example.com/",
		"https://example.com/",
		"http://192.0.2.10/",
		"http://localhost.example.com/",
		"https://user:pass@api.nextdns.io/",
		"http://user@127.0.0.1/",
		"https://api.nextdns.io/?x=1",
		"ftp://127.0.0.1/",
		"api.nextdns.io",
		"",
		"://bad",
	} {
		_, err := ValidateBaseURL(bad)
		c.True(errors.Is(err, ErrBaseURLNotAllowed))
	}

	_, err := New(WithBaseURL("https://example.com/"))
	c.True(errors.Is(err, ErrBaseURLNotAllowed))
}
