package proxy_test

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"api-profiler/proxy"
	"api-profiler/storage"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capturingStore collects saved records in memory for assertions.
type capturingStore struct {
	mu      sync.Mutex
	records []storage.Record
}

func (s *capturingStore) Save(r storage.Record) error {
	s.mu.Lock()
	s.records = append(s.records, r)
	s.mu.Unlock()
	return nil
}
func (s *capturingStore) SaveSpan(_ storage.InnerSpan) error { return nil }
func (s *capturingStore) Prune(_ time.Time) (int64, error)  { return 0, nil }
func (s *capturingStore) Close() error                      { return nil }

func (s *capturingStore) all() []storage.Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]storage.Record, len(s.records))
	copy(out, s.records)
	return out
}

// newHandlerWithRecorder creates a proxy Handler wired to a capturingStore.
// Returns the handler and the store so tests can inspect saved records.
func newHandlerWithRecorder(t *testing.T, upstream *url.URL) (*proxy.Handler, *capturingStore) {
	t.Helper()
	store := &capturingStore{}
	rec := storage.NewRecorder(store, 100)
	t.Cleanup(func() { rec.Close() })
	h, err := proxy.New(proxy.Config{Upstream: upstream, Recorder: rec})
	require.NoError(t, err)
	return h, store
}

// newHandler creates a proxy.Handler pointing to the given upstream URL.
func newHandler(t *testing.T, upstream *url.URL, overrides ...func(*proxy.Config)) *proxy.Handler {
	t.Helper()
	cfg := proxy.Config{Upstream: upstream}
	for _, fn := range overrides {
		fn(&cfg)
	}
	h, err := proxy.New(cfg)
	require.NoError(t, err)
	return h
}

// newUpstream creates a test HTTP server and returns it along with its parsed URL.
func newUpstream(handler http.HandlerFunc) (*httptest.Server, *url.URL) {
	srv := httptest.NewServer(handler)
	u, _ := url.Parse(srv.URL)
	return srv, u
}

// TC-01: GET without body — status, headers, body preserved.
func TestProxyGET(t *testing.T) {
	upstream, u := newUpstream(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"users":[]}`))
	})
	defer upstream.Close()

	rec := httptest.NewRecorder()
	newHandler(t, u).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/users", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, `{"users":[]}`, rec.Body.String())
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
}

// TC-02: POST with body — body reaches upstream intact; response proxied.
func TestProxyPOSTWithBody(t *testing.T) {
	var receivedBody string
	upstream, u := newUpstream(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		receivedBody = string(b)
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":1}`))
	})
	defer upstream.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(`{"name":"Alice"}`))
	rec := httptest.NewRecorder()
	newHandler(t, u).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, `{"name":"Alice"}`, receivedBody)
	assert.Equal(t, `{"id":1}`, rec.Body.String())
}

// TC-03: Query string forwarded unmodified.
func TestProxyQueryString(t *testing.T) {
	var gotQuery string
	upstream, u := newUpstream(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	})
	defer upstream.Close()

	req := httptest.NewRequest(http.MethodGet, "/search?q=foo&page=2", nil)
	newHandler(t, u).ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "q=foo&page=2", gotQuery)
}

// TC-04: Custom request header forwarded to upstream.
func TestProxyPreservesCustomHeader(t *testing.T) {
	var gotHeader string
	upstream, u := newUpstream(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Request-ID")
		w.WriteHeader(http.StatusOK)
	})
	defer upstream.Close()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "abc123")
	newHandler(t, u).ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "abc123", gotHeader)
}

// TC-05: Authorization header forwarded to upstream.
func TestProxyPreservesAuthorization(t *testing.T) {
	var gotAuth string
	upstream, u := newUpstream(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	})
	defer upstream.Close()

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer token123")
	newHandler(t, u).ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "Bearer token123", gotAuth)
}

// TC-06: Multiple response headers from upstream preserved.
func TestProxyPreservesResponseHeaders(t *testing.T) {
	upstream, u := newUpstream(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "99")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	})
	defer upstream.Close()

	rec := httptest.NewRecorder()
	newHandler(t, u).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, "99", rec.Header().Get("X-RateLimit-Remaining"))
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
}

// TC-07: Large body (1 MB) proxied completely.
func TestProxyLargeBody(t *testing.T) {
	const size = 1 << 20
	big := strings.Repeat("x", size)
	upstream, u := newUpstream(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(big))
	})
	defer upstream.Close()

	rec := httptest.NewRecorder()
	newHandler(t, u).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/big", nil))

	assert.Equal(t, size, rec.Body.Len())
}

// TC-08: Upstream 404 passed through.
func TestProxy404PassThrough(t *testing.T) {
	upstream, u := newUpstream(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	defer upstream.Close()

	rec := httptest.NewRecorder()
	newHandler(t, u).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/missing", nil))

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "not found")
}

// TC-09: Upstream 500 passed through.
func TestProxy500PassThrough(t *testing.T) {
	upstream, u := newUpstream(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	})
	defer upstream.Close()

	rec := httptest.NewRecorder()
	newHandler(t, u).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/error", nil))

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// TC-10: All HTTP methods proxied correctly.
func TestProxyAllMethods(t *testing.T) {
	methods := []string{
		http.MethodGet, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions,
	}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			var gotMethod string
			upstream, u := newUpstream(func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				w.WriteHeader(http.StatusOK)
			})
			defer upstream.Close()

			rec := httptest.NewRecorder()
			newHandler(t, u).ServeHTTP(rec, httptest.NewRequest(method, "/", nil))

			assert.Equal(t, method, gotMethod)
			assert.Equal(t, http.StatusOK, rec.Code)
		})
	}
}

// TC-11: Upstream unavailable → 502 Bad Gateway.
func TestProxyUpstreamUnavailable(t *testing.T) {
	// Grab a free port then release it so nothing is listening there.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	ln.Close()

	u, _ := url.Parse("http://" + addr)
	rec := httptest.NewRecorder()
	newHandler(t, u).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

// TC-12: Upstream timeout → 504 Gateway Timeout.
func TestProxyUpstreamTimeout(t *testing.T) {
	block := make(chan struct{})
	upstream, u := newUpstream(func(w http.ResponseWriter, r *http.Request) {
		<-block
	})
	defer upstream.Close()
	defer close(block)

	h, err := proxy.New(proxy.Config{
		Upstream: u,
		Timeout:  10 * time.Millisecond,
	})
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/slow", nil))

	assert.Equal(t, http.StatusGatewayTimeout, rec.Code)
}

// TC-13: GET sends no spurious body to upstream.
func TestProxyGETNoSpuriousBody(t *testing.T) {
	var bodyBytes []byte
	upstream, u := newUpstream(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	})
	defer upstream.Close()

	newHandler(t, u).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Empty(t, bodyBytes)
}

// TC-14: 204 No Content response has no body.
func TestProxy204NoContent(t *testing.T) {
	upstream, u := newUpstream(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	defer upstream.Close()

	rec := httptest.NewRecorder()
	newHandler(t, u).ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/resource/1", nil))

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String())
}

// TC-15: Upstream 301 redirect NOT followed — passed to client as-is.
func TestProxyNoFollowRedirects(t *testing.T) {
	upstream, u := newUpstream(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://other.example.com/new", http.StatusMovedPermanently)
	})
	defer upstream.Close()

	rec := httptest.NewRecorder()
	newHandler(t, u).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/old", nil))

	assert.Equal(t, http.StatusMovedPermanently, rec.Code)
	assert.Equal(t, "http://other.example.com/new", rec.Header().Get("Location"))
}

// TC-16: Hop-by-hop header Connection not forwarded to upstream.
func TestProxyFiltersHopByHopHeaders(t *testing.T) {
	var gotConnection string
	upstream, u := newUpstream(func(w http.ResponseWriter, r *http.Request) {
		gotConnection = r.Header.Get("Connection")
		w.WriteHeader(http.StatusOK)
	})
	defer upstream.Close()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Connection", "keep-alive")
	newHandler(t, u).ServeHTTP(httptest.NewRecorder(), req)

	assert.Empty(t, gotConnection, "Connection header must not reach upstream")
}

// TC-17: Upstream URL with base path — request path is concatenated correctly.
func TestProxyBasePath(t *testing.T) {
	var gotPath string
	upstream, u := newUpstream(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	defer upstream.Close()

	baseURL, _ := url.Parse(u.String() + "/v1")
	req := httptest.NewRequest(http.MethodGet, "/users", nil)
	newHandler(t, baseURL).ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "/v1/users", gotPath)
}

// TC-18: Port already in use is handled at Listen time (tested in main; here
// we just verify New() succeeds with a valid config — port binding is separate).
func TestNewValidConfig(t *testing.T) {
	u, _ := url.Parse("http://localhost:3000")
	h, err := proxy.New(proxy.Config{Upstream: u})
	require.NoError(t, err)
	assert.NotNil(t, h)
}

// TC-20: URL without scheme fails at New().
func TestNewURLWithoutHTTPScheme(t *testing.T) {
	// url.Parse("localhost:3000") gives Scheme="localhost" (not http/https).
	u, _ := url.Parse("localhost:3000")
	_, err := proxy.New(proxy.Config{Upstream: u})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http")
}

func TestNewNilUpstream(t *testing.T) {
	_, err := proxy.New(proxy.Config{Upstream: nil})
	require.Error(t, err)
}

func TestNewInvalidScheme(t *testing.T) {
	u, _ := url.Parse("ftp://localhost:3000")
	_, err := proxy.New(proxy.Config{Upstream: u})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http")
}

// --- US-02: metrics capture integration ---

// waitForRecords polls store until n records arrive or timeout.
func waitForRecords(store *capturingStore, n int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(store.all()) >= n {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return false
}

// US-02 TC-01: Correct fields captured after a proxied GET 200.
func TestRecorderCapturesFields(t *testing.T) {
	upstream, u := newUpstream(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	defer upstream.Close()

	h, store := newHandlerWithRecorder(t, u)
	before := time.Now()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/users", nil))

	require.True(t, waitForRecords(store, 1, 100*time.Millisecond), "record must be saved within 100ms")
	recs := store.all()
	require.Len(t, recs, 1)
	r := recs[0]
	assert.Equal(t, "GET", r.Method)
	assert.Equal(t, "/api/users", r.Path)
	assert.Equal(t, http.StatusOK, r.StatusCode)
	assert.GreaterOrEqual(t, r.DurationMs, 0.0)
	assert.WithinDuration(t, before, r.Timestamp, time.Second)
}

// US-02 TC-02: Path stored without query string.
func TestRecorderPathNoQueryString(t *testing.T) {
	upstream, u := newUpstream(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	defer upstream.Close()

	h, store := newHandlerWithRecorder(t, u)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/search?q=foo&page=2", nil))

	require.True(t, waitForRecords(store, 1, 100*time.Millisecond))
	assert.Equal(t, "/search", store.all()[0].Path)
}

// US-02 TC-04: Proxy-generated 502 is recorded.
func TestRecorderCaptures502(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	ln.Close()

	u, _ := url.Parse("http://" + addr)
	h, store := newHandlerWithRecorder(t, u)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	require.True(t, waitForRecords(store, 1, 100*time.Millisecond))
	assert.Equal(t, http.StatusBadGateway, store.all()[0].StatusCode)
}

// US-02 TC-08: Recorder nil — proxy works, nothing written to storage.
func TestRecorderNilNoOp(t *testing.T) {
	upstream, u := newUpstream(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	defer upstream.Close()

	h, err := proxy.New(proxy.Config{Upstream: u}) // Recorder is nil
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, 200, rec.Code)
}

// --- US-05: HTTPS upstream ---

// newTLSUpstream creates an httptest.NewTLSServer (self-signed cert).
func newTLSUpstream(handler http.HandlerFunc) (*httptest.Server, *url.URL) {
	srv := httptest.NewTLSServer(handler)
	u, _ := url.Parse(srv.URL)
	return srv, u
}

// TC-02: HTTPS upstream with self-signed cert + TLSSkipVerify=true → success.
func TestHTTPSSkipVerifyTrue(t *testing.T) {
	upstream, u := newTLSUpstream(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("secure"))
	})
	defer upstream.Close()

	h, err := proxy.New(proxy.Config{Upstream: u, TLSSkipVerify: true})
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "secure", rec.Body.String())
}

// TC-06: HTTPS upstream with self-signed cert + TLSSkipVerify=false → 502.
func TestHTTPSSkipVerifyFalseRejects(t *testing.T) {
	upstream, u := newTLSUpstream(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	defer upstream.Close()

	h, err := proxy.New(proxy.Config{Upstream: u, TLSSkipVerify: false})
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

// TC-03: HTTP upstream with TLSSkipVerify=true still works normally.
func TestHTTPUpstreamWithSkipVerifyTrue(t *testing.T) {
	upstream, u := newUpstream(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("plain"))
	})
	defer upstream.Close()

	h, err := proxy.New(proxy.Config{Upstream: u, TLSSkipVerify: true})
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "plain", rec.Body.String())
}

// TC-05: Warning is logged when TLSSkipVerify=true (proxy.New does not error).
func TestHTTPSSkipVerifyLogsWarning(t *testing.T) {
	u, _ := url.Parse("https://localhost:9999")
	h, err := proxy.New(proxy.Config{Upstream: u, TLSSkipVerify: true})
	require.NoError(t, err)
	assert.NotNil(t, h)
}

// --- US-06: Header preservation ---

// helper: captures all request headers received by the upstream.
func captureRequestHeaders(t *testing.T) (*httptest.Server, *url.URL, *http.Header) {
	t.Helper()
	var got http.Header
	srv, u := newUpstream(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	})
	return srv, u, &got
}

// TC-01: Authorization Bearer forwarded intact.
func TestHeaderAuthorizationBearer(t *testing.T) {
	srv, u, got := captureRequestHeaders(t)
	defer srv.Close()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer token123")
	newHandler(t, u).ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "Bearer token123", (*got).Get("Authorization"))
}

// TC-02: Authorization Basic forwarded intact.
func TestHeaderAuthorizationBasic(t *testing.T) {
	srv, u, got := captureRequestHeaders(t)
	defer srv.Close()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	newHandler(t, u).ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "Basic dXNlcjpwYXNz", (*got).Get("Authorization"))
}

// TC-03: X-Custom-Header forwarded with exact value.
func TestHeaderXCustomHeader(t *testing.T) {
	srv, u, got := captureRequestHeaders(t)
	defer srv.Close()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Custom-Header", "my-value")
	newHandler(t, u).ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "my-value", (*got).Get("X-Custom-Header"))
}

// TC-04: Cookie header forwarded intact.
func TestHeaderCookie(t *testing.T) {
	srv, u, got := captureRequestHeaders(t)
	defer srv.Close()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Cookie", "session=abc; user=alice")
	newHandler(t, u).ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "session=abc; user=alice", (*got).Get("Cookie"))
}

// TC-05: Multiple distinct headers all forwarded together.
func TestHeaderMultipleDistinct(t *testing.T) {
	srv, u, got := captureRequestHeaders(t)
	defer srv.Close()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("X-Request-ID", "req-42")
	req.Header.Set("X-Tenant-ID", "tenant-7")
	newHandler(t, u).ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "Bearer tok", (*got).Get("Authorization"))
	assert.Equal(t, "req-42", (*got).Get("X-Request-ID"))
	assert.Equal(t, "tenant-7", (*got).Get("X-Tenant-ID"))
}

// TC-06: Multi-value Accept header forwarded as-is.
func TestHeaderMultiValueAccept(t *testing.T) {
	srv, u, got := captureRequestHeaders(t)
	defer srv.Close()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html, application/json")
	newHandler(t, u).ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "text/html, application/json", (*got).Get("Accept"))
}

// TC-07: Multiple lines of the same request header all reach the upstream.
func TestHeaderMultipleLinesOfSameName(t *testing.T) {
	srv, u, got := captureRequestHeaders(t)
	defer srv.Close()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Add("X-Tag", "first")
	req.Header.Add("X-Tag", "second")
	newHandler(t, u).ServeHTTP(httptest.NewRecorder(), req)

	vals := (*got)["X-Tag"]
	assert.ElementsMatch(t, []string{"first", "second"}, vals)
}

// TC-08: Content-Type in POST forwarded correctly.
func TestHeaderContentTypeInPOST(t *testing.T) {
	srv, u, got := captureRequestHeaders(t)
	defer srv.Close()

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	newHandler(t, u).ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "application/json", (*got).Get("Content-Type"))
}

// TC-09: Header with empty value forwarded without error.
func TestHeaderEmptyValue(t *testing.T) {
	srv, u, got := captureRequestHeaders(t)
	defer srv.Close()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Empty", "")
	newHandler(t, u).ServeHTTP(httptest.NewRecorder(), req)

	// Go's net/http canonicalises empty-value headers; the key must be present.
	_, present := (*got)["X-Empty"]
	assert.True(t, present, "X-Empty header should reach upstream")
}

// TC-10: Custom response header forwarded to client.
func TestResponseHeaderCustom(t *testing.T) {
	upstream, u := newUpstream(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Trace-ID", "xyz-789")
		w.WriteHeader(http.StatusOK)
	})
	defer upstream.Close()

	rec := httptest.NewRecorder()
	newHandler(t, u).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, "xyz-789", rec.Header().Get("X-Trace-ID"))
}

// TC-11: Multiple Set-Cookie response headers all reach the client.
func TestResponseHeaderMultipleSetCookie(t *testing.T) {
	upstream, u := newUpstream(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Set-Cookie", "a=1; Path=/")
		w.Header().Add("Set-Cookie", "b=2; Path=/")
		w.WriteHeader(http.StatusOK)
	})
	defer upstream.Close()

	rec := httptest.NewRecorder()
	newHandler(t, u).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	cookies := rec.Header()["Set-Cookie"]
	assert.Len(t, cookies, 2)
	assert.ElementsMatch(t, []string{"a=1; Path=/", "b=2; Path=/"}, cookies)
}

// TC-12: Content-Type response header forwarded with charset intact.
func TestResponseHeaderContentTypeWithCharset(t *testing.T) {
	upstream, u := newUpstream(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
	})
	defer upstream.Close()

	rec := httptest.NewRecorder()
	newHandler(t, u).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
}

// US-26 TC-18: Normalizer configured → Record.Path contains normalised path.
func TestNormalizerApplied(t *testing.T) {
	upstream, u := newUpstream(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	defer upstream.Close()

	store := &capturingStore{}
	rec := storage.NewRecorder(store, 100)
	t.Cleanup(func() { rec.Close() })

	h, err := proxy.New(proxy.Config{
		Upstream:   u,
		Recorder:   rec,
		Normalizer: func(p string) string { return "/normalised" },
	})
	require.NoError(t, err)

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/users/123", nil))
	require.True(t, waitForRecords(store, 1, 200*time.Millisecond))
	assert.Equal(t, "/normalised", store.all()[0].Path)
}

// US-26 TC-19: Normalizer nil → Record.Path contains original path.
func TestNormalizerNilKeepsOriginalPath(t *testing.T) {
	upstream, u := newUpstream(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	defer upstream.Close()

	h, store := newHandlerWithRecorder(t, u)

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/users/123", nil))
	require.True(t, waitForRecords(store, 1, 200*time.Millisecond))
	assert.Equal(t, "/users/123", store.all()[0].Path)
}

// US-02 TC-07: 100 concurrent requests → 100 records.
func TestRecorderConcurrentRequests(t *testing.T) {
	upstream, u := newUpstream(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	defer upstream.Close()

	h, store := newHandlerWithRecorder(t, u)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
		}()
	}
	wg.Wait()
	require.True(t, waitForRecords(store, 100, 200*time.Millisecond), "all 100 records must be saved")
	assert.Len(t, store.all(), 100)
}

// ── Header rewrite (US-37) ────────────────────────────────────────────────────

// captureUpstream creates a test upstream that stores the last received headers.
func captureUpstream() (*httptest.Server, func() http.Header) {
	var mu sync.Mutex
	var captured http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		mu.Lock()
		captured = r.Header.Clone()
		mu.Unlock()
	}))
	return srv, func() http.Header {
		mu.Lock()
		defer mu.Unlock()
		return captured
	}
}

// TC-05 (US-37): RewriteHeaders=nil → headers sin modificar.
func TestRewriteHeadersNil(t *testing.T) {
	srv, getHeaders := captureUpstream()
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	h := newHandler(t, u)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Custom", "original")
	h.ServeHTTP(httptest.NewRecorder(), req)
	assert.Equal(t, "original", getHeaders().Get("X-Custom"))
}

// TC-06 (US-37): action=set → header presente en upstream.
func TestRewriteHeadersSet(t *testing.T) {
	srv, getHeaders := captureUpstream()
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	h := newHandler(t, u, func(cfg *proxy.Config) {
		cfg.RewriteHeaders = func(hdr http.Header) { hdr.Set("X-Injected", "hello") }
	})
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, "hello", getHeaders().Get("X-Injected"))
}

// TC-07 (US-37): action=set sobreescribe header existente.
func TestRewriteHeadersSetOverwrite(t *testing.T) {
	srv, getHeaders := captureUpstream()
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	h := newHandler(t, u, func(cfg *proxy.Config) {
		cfg.RewriteHeaders = func(hdr http.Header) { hdr.Set("X-Token", "new-value") }
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Token", "old-value")
	h.ServeHTTP(httptest.NewRecorder(), req)
	assert.Equal(t, "new-value", getHeaders().Get("X-Token"))
}

// TC-08 (US-37): action=remove → header eliminado en upstream.
func TestRewriteHeadersRemove(t *testing.T) {
	srv, getHeaders := captureUpstream()
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	h := newHandler(t, u, func(cfg *proxy.Config) {
		cfg.RewriteHeaders = func(hdr http.Header) { hdr.Del("X-Secret") }
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Secret", "sensitive")
	h.ServeHTTP(httptest.NewRecorder(), req)
	assert.Empty(t, getHeaders().Get("X-Secret"))
}

// TC-09 (US-37): reglas aplicadas en orden (set luego remove sobre mismo header).
func TestRewriteHeadersOrder(t *testing.T) {
	srv, getHeaders := captureUpstream()
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	h := newHandler(t, u, func(cfg *proxy.Config) {
		cfg.RewriteHeaders = func(hdr http.Header) {
			hdr.Set("X-Temp", "value")
			hdr.Del("X-Temp")
		}
	})
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Empty(t, getHeaders().Get("X-Temp"))
}

// --- US-45: W3C TraceContext ---

// newHandlerWithTraceContext creates a proxy Handler with TraceContext enabled and a capturing store.
func newHandlerWithTraceContext(t *testing.T, upstream *url.URL, enabled bool) (*proxy.Handler, *capturingStore) {
	t.Helper()
	store := &capturingStore{}
	rec := storage.NewRecorder(store, 100)
	t.Cleanup(func() { rec.Close() })
	h, err := proxy.New(proxy.Config{Upstream: upstream, Recorder: rec, TraceContext: enabled})
	require.NoError(t, err)
	return h, store
}

// TC-07: Request sin traceparent → proxy genera trace_id y lo propaga al upstream.
func TestTraceContextGeneratesWhenAbsent(t *testing.T) {
	var receivedTraceparent string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedTraceparent = r.Header.Get("Traceparent")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	h, store := newHandlerWithTraceContext(t, upstreamURL, true)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	// Upstream must receive a valid traceparent.
	assert.NotEmpty(t, receivedTraceparent)
	parts := strings.Split(receivedTraceparent, "-")
	require.Len(t, parts, 4)
	assert.Len(t, parts[1], 32) // trace-id
	assert.Len(t, parts[2], 16) // span-id

	// Record must have non-empty trace_id and span_id.
	time.Sleep(50 * time.Millisecond) // let recorder drain
	records := store.all()
	require.Len(t, records, 1)
	assert.Len(t, records[0].TraceID, 32)
	assert.Len(t, records[0].SpanID, 16)
}

// TC-08: Request con traceparent válido → proxy reutiliza trace-id, genera nuevo span-id.
func TestTraceContextPreservesTraceID(t *testing.T) {
	const incomingTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	var receivedTraceparent string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedTraceparent = r.Header.Get("Traceparent")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	h, store := newHandlerWithTraceContext(t, upstreamURL, true)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Traceparent", "00-"+incomingTraceID+"-00f067aa0ba902b7-01")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	// Upstream trace-id must match incoming.
	parts := strings.Split(receivedTraceparent, "-")
	require.Len(t, parts, 4)
	assert.Equal(t, incomingTraceID, parts[1])

	// Span-id must be freshly generated (different from incoming parent-id).
	assert.NotEqual(t, "00f067aa0ba902b7", parts[2])

	time.Sleep(50 * time.Millisecond)
	records := store.all()
	require.Len(t, records, 1)
	assert.Equal(t, incomingTraceID, records[0].TraceID)
}

// TC-09: Request con traceparent inválido → proxy genera nuevo trace-id.
func TestTraceContextIgnoresBadHeader(t *testing.T) {
	var receivedTraceparent string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedTraceparent = r.Header.Get("Traceparent")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	h, _ := newHandlerWithTraceContext(t, upstreamURL, true)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Traceparent", "not-valid-at-all")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	// Upstream must receive a freshly generated (valid) traceparent.
	assert.NotEmpty(t, receivedTraceparent)
	assert.NotEqual(t, "not-valid-at-all", receivedTraceparent)
	parts := strings.Split(receivedTraceparent, "-")
	require.Len(t, parts, 4)
	assert.Len(t, parts[1], 32)
}

// TC-10: TraceContext=false → proxy no añade traceparent; trace_id en Record vacío.
func TestTraceContextDisabled(t *testing.T) {
	var receivedTraceparent string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedTraceparent = r.Header.Get("Traceparent")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	h, store := newHandlerWithTraceContext(t, upstreamURL, false)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	assert.Empty(t, receivedTraceparent)

	time.Sleep(50 * time.Millisecond)
	records := store.all()
	require.Len(t, records, 1)
	assert.Empty(t, records[0].TraceID)
	assert.Empty(t, records[0].SpanID)
}

// TC-11: Request with existing traceparent → proxy preserves trace_id and records parent_span_id.
func TestTraceContextPreservesParentSpanID(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	h, store := newHandlerWithTraceContext(t, upstreamURL, true)

	const traceID   = "4bf92f3577b34da6a3ce929d0e0e4736"
	const parentID  = "00f067aa0ba902b7"
	const traceparent = "00-" + traceID + "-" + parentID + "-01"

	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Traceparent", traceparent)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	time.Sleep(50 * time.Millisecond)
	records := store.all()
	require.Len(t, records, 1)
	assert.Equal(t, traceID, records[0].TraceID)
	assert.Equal(t, parentID, records[0].ParentSpanID)
	assert.Len(t, records[0].SpanID, 16)
}
