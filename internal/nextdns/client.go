package nextdns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/go-cleanhttp"
)

const (
	baseURL     = "https://api.nextdns.io/"
	contentType = "application/json"
	userAgent   = "nextdns-go"
)

// Retry policy for 429 and 5xx responses (see Client.do).
const (
	// defaultMaxAttempts is the total number of attempts, the first included.
	defaultMaxAttempts = 4
	// defaultFirstRetryWait is the backoff before the first retry; it doubles on
	// each further retry.
	defaultFirstRetryWait = time.Second
	// defaultRetryWaitCap caps the backoff and the Retry-After the client is
	// willing to honour. A longer Retry-After ends the retries and returns
	// the error rather than retrying early.
	defaultRetryWaitCap = time.Minute
)

var (
	// ErrRedirectRefused is returned when the API answers with a redirect.
	// The API key is added to every request, so following a redirect would
	// send it to wherever the redirect points.
	ErrRedirectRefused = errors.New("refusing to follow an HTTP redirect")

	// ErrBaseURLNotAllowed is returned for a base URL other than the
	// production API or a loopback address. The API key is sent to the base
	// URL, so it must not point anywhere else.
	ErrBaseURLNotAllowed = errors.New("the base URL must be https://api.nextdns.io/ or a loopback URL such as http://127.0.0.1:<port>/ or http://localhost:<port>/")
)

// Client represents a NextDNS client.
type Client struct {
	client  *http.Client
	baseURL *url.URL

	// Service for the Profile.
	Profiles ProfilesService

	// Services for the Allowlist and Denylist.
	Allowlist AllowlistService
	Denylist  DenylistService

	// Services for the ParentalControl.
	ParentalControl           ParentalControlService
	ParentalControlServices   ParentalControlServicesService
	ParentalControlCategories ParentalControlCategoriesService

	// Services for the Privacy.
	Privacy           PrivacyService
	PrivacyBlocklists PrivacyBlocklistsService
	PrivacyNatives    PrivacyNativesService

	// Services for the Settings.
	Settings            SettingsService
	SettingsLogs        SettingsLogsService
	SettingsBlockPage   SettingsBlockPageService
	SettingsPerformance SettingsPerformanceService

	// Services for the Security.
	Security     SecurityService
	SecurityTlds SecurityTldsService

	// Services for the Rewrites.
	Rewrites RewritesService

	// Services for the Setup.
	Setup         SetupService
	SetupLinkedIP SetupLinkedIPService

	// Debug mode for the HTTP requests.
	Debug bool

	// Retry policy (see do). Unexported so that tests in this package can
	// shorten the waits.
	maxAttempts    int
	firstRetryWait time.Duration
	retryWaitCap   time.Duration
}

// ClientOption is a function that can be used to customize the client.
type ClientOption func(c *Client) error

// WithBaseURL sets the base URL of the NextDNS API. Only the production API
// and loopback URLs are accepted (see ValidateBaseURL).
func WithBaseURL(baseURL string) ClientOption {
	return func(c *Client) error {
		parsedURL, err := ValidateBaseURL(baseURL)
		if err != nil {
			return err
		}

		c.baseURL = parsedURL
		return nil
	}
}

// ValidateBaseURL parses raw and checks that it may be used as the API base
// URL: https://api.nextdns.io/ itself, or an http or https URL on a loopback
// host (localhost, 127.0.0.0/8, ::1) for stub servers in tests. The API key
// is sent to the base URL, so anything else is refused, as are URLs with
// credentials, a query or a fragment.
func ValidateBaseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBaseURLNotAllowed, err)
	}
	if u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("%w (credentials, a query or a fragment are not allowed)", ErrBaseURLNotAllowed)
	}
	switch {
	case u.Scheme == "https" && u.Host == "api.nextdns.io" && (u.Path == "" || u.Path == "/"):
		return u, nil
	case (u.Scheme == "http" || u.Scheme == "https") && isLoopbackHost(u.Hostname()):
		return u, nil
	}
	return nil, fmt.Errorf("%w (got %s://%s%s)", ErrBaseURLNotAllowed, u.Scheme, u.Host, u.Path)
}

// isLoopbackHost reports whether host is localhost or a loopback IP address.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// WithAPIKey sets the API key to be used for requests.
func WithAPIKey(apiKey string) ClientOption {
	return func(c *Client) error {
		if apiKey == "" {
			return ErrEmptyAPIToken
		}

		transport := authTransport{
			rt:     c.client.Transport,
			apiKey: apiKey,
		}

		c.client.Transport = &transport
		return nil
	}
}

// WithDebug enables debug mode.
func WithDebug() ClientOption {
	return func(c *Client) error {
		c.Debug = true
		return nil
	}
}

// WithHTTPClient sets a custom HTTP client that can be used for requests.
// New sets its CheckRedirect so that redirects are refused.
func WithHTTPClient(client *http.Client) ClientOption {
	return func(c *Client) error {
		if client == nil {
			client = cleanhttp.DefaultClient()
		}

		c.client = client
		return nil
	}
}

// New instantiates a new NextDNS client.
func New(opts ...ClientOption) (*Client, error) {
	baseURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}

	c := &Client{
		client:         cleanhttp.DefaultClient(),
		baseURL:        baseURL,
		maxAttempts:    defaultMaxAttempts,
		firstRetryWait: defaultFirstRetryWait,
		retryWaitCap:   defaultRetryWaitCap,
	}

	for _, opt := range opts {
		err := opt(c)
		if err != nil {
			return nil, err
		}
	}

	// Never follow a redirect: the API key would go along with it.
	c.client.CheckRedirect = refuseRedirect

	// Initialize the services for the Profile.
	c.Profiles = NewProfilesService(c)

	// Initialize the services for the Allowlist and Denylist.
	c.Allowlist = NewAllowlistService(c)
	c.Denylist = NewDenylistService(c)

	// Initialize the services for the ParentalControl.
	c.ParentalControl = NewParentalControlService(c)
	c.ParentalControlServices = NewParentalControlServicesService(c)
	c.ParentalControlCategories = NewParentalControlCategoriesService(c)

	// Initialize the services for the Privacy.
	c.Privacy = NewPrivacyService(c)
	c.PrivacyBlocklists = NewPrivacyBlocklistsService(c)
	c.PrivacyNatives = NewPrivacyNativesService(c)

	// Initialize the services for the Settings.
	c.Settings = NewSettingsService(c)
	c.SettingsLogs = NewSettingsLogsService(c)
	c.SettingsBlockPage = NewSettingsBlockPageService(c)
	c.SettingsPerformance = NewSettingsPerformanceService(c)

	// Initialize the services for the Security.
	c.Security = NewSecurityService(c)
	c.SecurityTlds = NewSecurityTldsService(c)

	// Initialize the services for the Rewrites.
	c.Rewrites = NewRewritesService(c)

	// Initialize the services for the Setup.
	c.Setup = NewSetupService(c)
	c.SetupLinkedIP = NewSetupLinkedIPService(c)

	return c, nil
}

// do executes an HTTP request and decodes the response into v.
//
// A 429 or 5xx response is retried with exponential backoff and jitter, up
// to maxAttempts attempts in all, waiting at least as long as a Retry-After
// header asks. A 429 is retried for every method, because the request was
// refused before it was processed. A 5xx is retried only for GET, PUT, PATCH
// and DELETE: a POST that failed with a 5xx may still have been applied, and
// repeating it could create a duplicate. Transport errors are not retried.
// The last response is handled as usual.
func (c *Client) do(ctx context.Context, req *http.Request, v interface{}) error {
	for attempt := 1; ; attempt++ {
		res, err := c.send(ctx, req, attempt)
		if err != nil {
			return err
		}

		if wait, retry := c.retryWait(req.Method, res, attempt); retry {
			// Drain some of the body so the connection can be reused.
			_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
			_ = res.Body.Close()
			if err := sleepContext(ctx, wait); err != nil {
				return err
			}
			continue
		}

		err = c.handleResponse(ctx, res, v)
		_ = res.Body.Close()
		return err
	}
}

// send performs one attempt of req. Every attempt gets its own copy of the
// request; a retry gets a fresh body from GetBody, which http.NewRequest
// sets for the in-memory bodies newRequest builds.
func (c *Client) send(ctx context.Context, req *http.Request, attempt int) (*http.Response, error) {
	try := req.Clone(ctx)
	if attempt > 1 && req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		try.Body = body
	}
	return c.client.Do(try)
}

// retryWait reports whether res should be retried and how long to wait
// before the next attempt.
func (c *Client) retryWait(method string, res *http.Response, attempt int) (time.Duration, bool) {
	if attempt >= c.maxAttempts {
		return 0, false
	}
	switch {
	case res.StatusCode == http.StatusTooManyRequests:
	case res.StatusCode >= http.StatusInternalServerError && idempotent(method):
	default:
		return 0, false
	}

	wait := c.backoff(attempt)
	if after, ok := parseRetryAfter(res.Header.Get("Retry-After"), time.Now()); ok {
		if after > c.retryWaitCap {
			// Retrying sooner than asked would only be refused again.
			return 0, false
		}
		wait = max(wait, after)
	}
	return wait, true
}

// backoff returns the wait before retry number attempt (1-based):
// firstRetryWait doubled per retry and capped at retryWaitCap, with equal
// jitter (half fixed, half random) so that concurrent callers spread out.
func (c *Client) backoff(attempt int) time.Duration {
	d := c.retryWaitCap
	if attempt >= 1 && attempt <= 32 {
		if exp := c.firstRetryWait << (attempt - 1); exp > 0 && exp < d {
			d = exp
		}
	}
	half := d / 2
	return half + rand.N(half+1) //nolint:gosec // jitter, not security sensitive
}

// idempotent reports whether a request with this method can be repeated
// without changing the outcome.
func idempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// parseRetryAfter parses a Retry-After header, either delay-seconds or an
// HTTP date relative to now. It reports false when the header is absent or
// malformed.
func parseRetryAfter(v string, now time.Time) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.ParseInt(v, 10, 64); err == nil {
		switch {
		case secs < 0:
			return 0, true
		case secs > int64(24*time.Hour/time.Second):
			return 24 * time.Hour, true
		}
		return time.Duration(secs) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(t.Sub(now), 0), true
	}
	return 0, false
}

// sleepContext waits for d, or returns early with the context's error.
func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// refuseRedirect is the http.Client CheckRedirect hook: every redirect is
// refused, so the API key never follows one to another URL.
func refuseRedirect(req *http.Request, _ []*http.Request) error {
	return fmt.Errorf("%w to %s://%s", ErrRedirectRefused, req.URL.Scheme, req.URL.Host)
}

// handleResponse handles the response from the NextDNS API and decodes the response into v if provided.
// The goal is to handle the common errors that can occur when making a request to the NextDNS API,
// and also provide custom error responses for the client.
func (c *Client) handleResponse(_ context.Context, res *http.Response, v interface{}) error {
	out, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}

	if c.Debug {
		if string(out) == "" {
			fmt.Printf("[DEBUG] RESPONSE: StatusCode:%d\n", res.StatusCode)
		} else {
			fmt.Printf("[DEBUG] RESPONSE: StatusCode:%d, Body:%v\n", res.StatusCode, string(out))
		}
	}

	// If there is no response body, then we don't need to do anything.
	if res.StatusCode == http.StatusNoContent {
		return nil
	}

	// Sets some default additional information that can be used by the client to debug the error.
	meta := map[string]string{
		"body":        string(out),
		"http_status": http.StatusText(res.StatusCode),
	}

	// If the response is not a 200, then we need to handle the error.
	// TODO(amalucelli): Report the behavior to NextDNS, but there are errors that return HTTP 200 ("duplicate" case).
	if res.StatusCode >= http.StatusBadRequest || strings.Contains(string(out), "\"errors\"") {
		if res.StatusCode >= http.StatusInternalServerError {
			return &Error{
				Type:       ErrorTypeServiceError,
				Message:    errInternalServiceError,
				Errors:     nil,
				Meta:       meta,
				StatusCode: res.StatusCode,
			}
		}

		// Tries to handle the error response body from the NextDNS API,
		// encapsulated in a client error.
		errorRes := &ErrorResponse{}
		err = json.Unmarshal(out, errorRes)
		if err != nil {
			var jsonErr *json.SyntaxError
			if errors.As(err, &jsonErr) {
				meta["err"] = jsonErr.Error()
				return &Error{
					Type:       ErrorTypeMalformed,
					Message:    errMalformedErrorBody,
					Errors:     nil,
					Meta:       meta,
					StatusCode: res.StatusCode,
				}
			}
			return err
		}

		// Sets custom error messages for the client based on the HTTP status code.
		var errType ErrorType

		switch res.StatusCode {
		case http.StatusForbidden:
			errType = ErrorTypeAuthentication
		case http.StatusNotFound:
			errType = ErrorTypeNotFound
		default:
			errType = ErrorTypeRequest
		}

		// Returns the error response from the NextDNS API encapsulated in a client error.
		return &Error{
			Type:       errType,
			Message:    errResponseError,
			Errors:     errorRes,
			Meta:       meta,
			StatusCode: res.StatusCode,
		}
	}

	// Returns if there is no object to decode.
	if v == nil {
		return nil
	}

	// Decodes the response body into the provided object.
	err = json.Unmarshal(out, &v)
	if err != nil {
		var jsonErr *json.SyntaxError
		if errors.As(err, &jsonErr) {
			meta["err"] = jsonErr.Error()
			return &Error{
				Type:       ErrorTypeMalformed,
				Message:    errMalformedError,
				Errors:     nil,
				Meta:       meta,
				StatusCode: res.StatusCode,
			}
		}
		return err
	}

	return nil
}

// newRequest creates a new HTTP request.
func (c *Client) newRequest(method string, path string, body interface{}) (*http.Request, error) {
	u, err := c.baseURL.Parse(path)
	if err != nil {
		return nil, err
	}

	var req *http.Request
	switch method {
	case http.MethodGet:
		if c.Debug {
			fmt.Printf("[DEBUG] REQUEST: Method:%s, URL:%s\n", method, u.String())
		}
		req, err = http.NewRequest(method, u.String(), nil)
		if err != nil {
			return nil, err
		}
	default:
		buf := new(bytes.Buffer)
		if body != nil {
			err = json.NewEncoder(buf).Encode(body)
			if err != nil {
				return nil, err
			}
		}
		if c.Debug {
			if buf.String() == "" {
				fmt.Printf("[DEBUG] REQUEST: Method:%s, URL:%s\n", method, u.String())
			} else {
				fmt.Printf("[DEBUG] REQUEST: Method:%s, URL:%s, Body:%s\n", method, u.String(), strings.TrimSuffix(buf.String(), "\n"))
			}
		}
		req, err = http.NewRequest(method, u.String(), buf)
		if err != nil {
			return nil, err
		}

		req.Header.Set("Content-Type", contentType)
	}

	req.Header.Set("Accept", contentType)
	req.Header.Set("User-Agent", userAgent)
	return req, nil
}

// authHeader represents a RoundTripper that adds an authorization header to the request.
type authTransport struct {
	rt     http.RoundTripper
	apiKey string
}

// RoundTrip adds the authorization header to requests. A RoundTripper must
// not modify the request it is given, so the header is set on a copy, and a
// retried request never carries the key twice.
func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("X-Api-Key", t.apiKey)
	return t.rt.RoundTrip(req)
}
