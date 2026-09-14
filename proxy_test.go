package proxy

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestPathMatches tests the logic for route matching
func TestPathMatches(t *testing.T) {
	tests := []struct {
		name        string
		requestPath string
		routePath   string
		want        bool
	}{
		{"exact match", "/exact", "/exact", true},
		{"exact route ignores subpaths", "/exact/extra", "/exact", false},
		{"unrelated path", "/other", "/exact", false},
		{"wildcard matches child", "/prefix/resource", "/prefix/*", true},
		{"wildcard matches deep child", "/prefix/a/b/c", "/prefix/*", true},
		{"wildcard matches bare prefix", "/prefix", "/prefix/*", true},
		{"wildcard matches prefix with slash", "/prefix/", "/prefix/*", true},
		{"wildcard does not match longer segment", "/prefixfoo", "/prefix/*", false},
		{"wildcard does not match sibling", "/prefix-other/a", "/prefix/*", false},
		{"root wildcard matches everything", "/anything", "/*", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pathMatches(tt.requestPath, tt.routePath); got != tt.want {
				t.Errorf("pathMatches(%q, %q) = %v, want %v", tt.requestPath, tt.routePath, got, tt.want)
			}
		})
	}
}

// TestMethodAllowed tests the HTTP method checking logic
func TestMethodAllowed(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		allowed []string
		want    bool
	}{
		{"listed method", "GET", []string{"GET", "POST"}, true},
		{"case insensitive", "post", []string{"GET", "POST"}, true},
		{"unlisted method", "DELETE", []string{"GET", "POST"}, false},
		{"empty allow list", "GET", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := methodAllowed(tt.method, tt.allowed); got != tt.want {
				t.Errorf("methodAllowed(%q, %v) = %v, want %v", tt.method, tt.allowed, got, tt.want)
			}
		})
	}
}

// TestRewritePath tests how request paths are mapped onto the target
func TestRewritePath(t *testing.T) {
	tests := []struct {
		name        string
		requestPath string
		routePath   string
		targetPath  string
		want        string
	}{
		{"exact route with target path", "/svc", "/svc", "/upstream", "/upstream"},
		{"exact route without target path", "/svc", "/svc", "", "/"},
		{"wildcard appends remainder", "/api/users/1", "/api/*", "/upstream", "/upstream/users/1"},
		{"wildcard onto bare target", "/api/users/1", "/api/*", "", "/users/1"},
		{"wildcard on bare prefix", "/api", "/api/*", "/upstream", "/upstream"},
		{"wildcard strips trailing slash on target", "/api/users", "/api/*", "/upstream/", "/upstream/users"},
		{"root wildcard", "/a/b", "/*", "/base", "/base/a/b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rewritePath(tt.requestPath, tt.routePath, tt.targetPath)
			if got != tt.want {
				t.Errorf("rewritePath(%q, %q, %q) = %q, want %q",
					tt.requestPath, tt.routePath, tt.targetPath, got, tt.want)
			}
		})
	}
}

// TestFindRoute_Specificity verifies that a wildcard never shadows a more
// specific route, whatever order the routes appear in the configuration.
func TestFindRoute_Specificity(t *testing.T) {
	server := mustServer(t, &Config{
		Routes: []Route{
			{Path: "/api/*", Target: "http://wildcard.example.com"},
			{Path: "/api/users", Target: "http://users.example.com"},
			{Path: "/api/users/*", Target: "http://users-tree.example.com"},
		},
	})

	tests := []struct {
		name        string
		requestPath string
		want        string
	}{
		{"exact route beats wildcard", "/api/users", "/api/users"},
		{"longest wildcard wins", "/api/users/42", "/api/users/*"},
		{"shorter wildcard still reachable", "/api/orders", "/api/*"},
		{"no partial-segment match", "/apifoo", ""},
		{"unmatched path", "/other", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := server.findRoute(tt.requestPath)
			got := ""
			if entry != nil {
				got = entry.route.Path
			}
			if got != tt.want {
				t.Errorf("findRoute(%q) = %q, want %q", tt.requestPath, got, tt.want)
			}
		})
	}
}

// TestNewServer_Errors verifies that broken routes fail at startup rather than
// turning into a 500 on the first request.
func TestNewServer_Errors(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantMsg string
	}{
		{name: "nil config", config: nil, wantMsg: "must not be nil"},
		{
			name:    "empty path",
			config:  &Config{Routes: []Route{{Path: "", Target: "http://example.com"}}},
			wantMsg: "empty path",
		},
		{
			name:    "unparsable target",
			config:  &Config{Routes: []Route{{Path: "/a", Target: "http://[::1]:namedport"}}},
			wantMsg: "invalid target",
		},
		{
			name:    "target without scheme",
			config:  &Config{Routes: []Route{{Path: "/a", Target: "example.com"}}},
			wantMsg: "must include scheme and host",
		},
		{
			name:    "empty target",
			config:  &Config{Routes: []Route{{Path: "/a", Target: ""}}},
			wantMsg: "must include scheme and host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewServer(tt.config)
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("error = %q, want it to mention %q", err, tt.wantMsg)
			}
		})
	}
}

const jsonConfig = `{
  "server": {"port": 9090, "timeout": 15},
  "routes": [
    {
      "path": "/api/*",
      "target": "http://api.example.com",
      "methods": ["GET", "POST"],
      "header_rules": [{"from_header": "X-User-ID", "to_query": "user", "remove": true}],
      "add_headers": {"X-Proxy": "simple-proxy"},
      "required_headers": ["X-Company-ID"]
    }
  ]
}`

// TestLoadConfigFromJSONString covers parsing a configuration held as JSON
func TestLoadConfigFromJSONString(t *testing.T) {
	t.Run("full configuration", func(t *testing.T) {
		config, err := LoadConfigFromJSONString(jsonConfig)
		if err != nil {
			t.Fatalf("LoadConfigFromJSONString() error = %v", err)
		}

		want := &Config{
			Server: ServerConfig{Port: 9090, Timeout: 15},
			Routes: []Route{{
				Path:            "/api/*",
				Target:          "http://api.example.com",
				Methods:         []string{"GET", "POST"},
				HeaderRules:     []HeaderRule{{FromHeader: "X-User-ID", ToQuery: "user", Remove: true}},
				AddHeaders:      map[string]string{"X-Proxy": "simple-proxy"},
				RequiredHeaders: []string{"X-Company-ID"},
			}},
		}
		if !reflect.DeepEqual(config, want) {
			t.Errorf("config = %+v, want %+v", config, want)
		}
	})

	t.Run("defaults applied", func(t *testing.T) {
		config, err := LoadConfigFromJSONString(`{"routes": [{"path": "/a", "target": "http://a.example.com"}]}`)
		if err != nil {
			t.Fatalf("LoadConfigFromJSONString() error = %v", err)
		}
		if config.Server.Port != 8000 || config.Server.Timeout != 30 {
			t.Errorf("server config = %+v, want port 8000 and timeout 30", config.Server)
		}
	})

	errorTests := []struct {
		name    string
		data    string
		wantMsg string
	}{
		{"empty string", "", "empty JSON configuration"},
		{"blank string", "   \n\t ", "empty JSON configuration"},
		{"malformed JSON", `{"server": {`, "invalid JSON configuration"},
		{"wrong type", `{"server": {"port": "8080"}}`, "invalid JSON configuration"},
	}

	for _, tt := range errorTests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadConfigFromJSONString(tt.data)
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("error = %q, want it to mention %q", err, tt.wantMsg)
			}
		})
	}
}

// TestLoadConfigFromEnv covers reading the JSON configuration from the environment
func TestLoadConfigFromEnv(t *testing.T) {
	t.Run("valid environment variable", func(t *testing.T) {
		t.Setenv("PROXY_CONFIG", jsonConfig)

		config, err := LoadConfigFromEnv("PROXY_CONFIG")
		if err != nil {
			t.Fatalf("LoadConfigFromEnv() error = %v", err)
		}

		want, err := LoadConfigFromJSONString(jsonConfig)
		if err != nil {
			t.Fatalf("LoadConfigFromJSONString() error = %v", err)
		}
		if !reflect.DeepEqual(config, want) {
			t.Errorf("config = %+v, want %+v", config, want)
		}
	})

	t.Run("empty variable name", func(t *testing.T) {
		_, err := LoadConfigFromEnv("")
		if err == nil || !strings.Contains(err.Error(), "must not be empty") {
			t.Errorf("error = %v, want it to mention an empty variable name", err)
		}
	})

	t.Run("variable set but empty", func(t *testing.T) {
		t.Setenv("PROXY_CONFIG", "  ")

		_, err := LoadConfigFromEnv("PROXY_CONFIG")
		if err == nil || !strings.Contains(err.Error(), "is not set or empty") {
			t.Errorf("error = %v, want it to mention an unset or empty variable", err)
		}
	})

	t.Run("variable not set", func(t *testing.T) {
		_, err := LoadConfigFromEnv("PROXY_CONFIG_UNSET_FOR_TEST")
		if err == nil || !strings.Contains(err.Error(), "is not set or empty") {
			t.Errorf("error = %v, want it to mention an unset or empty variable", err)
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		t.Setenv("PROXY_CONFIG", "{")

		_, err := LoadConfigFromEnv("PROXY_CONFIG")
		if err == nil {
			t.Fatal("expected an error, got nil")
		}
		if !strings.Contains(err.Error(), "PROXY_CONFIG") || !strings.Contains(err.Error(), "invalid JSON configuration") {
			t.Errorf("error = %q, want it to name the variable and the JSON failure", err)
		}
	})
}

// TestLoadConfig_JSONAndYAMLEquivalence verifies that the dual struct tags make
// both formats describe the same configuration.
func TestLoadConfig_JSONAndYAMLEquivalence(t *testing.T) {
	yamlConfig := `
server:
  port: 9090
  timeout: 15
routes:
  - path: /api/*
    target: http://api.example.com
    methods:
      - GET
      - POST
    header_rules:
      - from_header: X-User-ID
        to_query: user
        remove: true
    add_headers:
      X-Proxy: simple-proxy
    required_headers:
      - X-Company-ID
`

	fromYAML, err := LoadConfigFromBytes([]byte(yamlConfig))
	if err != nil {
		t.Fatalf("LoadConfigFromBytes() error = %v", err)
	}
	fromJSON, err := LoadConfigFromJSONString(jsonConfig)
	if err != nil {
		t.Fatalf("LoadConfigFromJSONString() error = %v", err)
	}

	if !reflect.DeepEqual(fromYAML, fromJSON) {
		t.Errorf("YAML config = %+v, JSON config = %+v, want them to be equal", fromYAML, fromJSON)
	}
}

// TestServeHTTP_Proxying tests the full proxying flow including modifications
func TestServeHTTP_Proxying(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/upstream/resource" {
			t.Errorf("upstream path = %s, want /upstream/resource", r.URL.Path)
		}
		if q := r.URL.Query().Get("user"); q != "123" {
			t.Errorf("query param user = %s, want 123", q)
		}
		if r.Header.Get("X-User-ID") != "" {
			t.Error("expected X-User-ID header to be removed")
		}
		if got := r.Header.Get("X-Proxy-Add"); got != "added" {
			t.Errorf("X-Proxy-Add = %s, want added", got)
		}

		w.Header().Set("X-Upstream-Response", "ok")
		w.Write([]byte("response from upstream"))
	}))
	defer upstream.Close()

	server := mustServer(t, &Config{
		Server: ServerConfig{Timeout: 5},
		Routes: []Route{
			{
				Path:       "/api/*",
				Target:     upstream.URL + "/upstream",
				Methods:    []string{"GET"},
				AddHeaders: map[string]string{"X-Proxy-Add": "added"},
				HeaderRules: []HeaderRule{
					{FromHeader: "X-User-ID", ToQuery: "user", Remove: true},
				},
			},
		},
	})

	req := httptest.NewRequest("GET", "/api/resource", nil)
	req.Header.Set("X-User-ID", "123")
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "response from upstream" {
		t.Errorf("body = %q, want %q", body, "response from upstream")
	}
	if resp.Header.Get("X-Upstream-Response") != "ok" {
		t.Error("expected X-Upstream-Response header to be forwarded")
	}
}

// TestServeHTTP_PreservesQueryString verifies that a request query survives
// proxying when the route defines no header rules.
func TestServeHTTP_PreservesQueryString(t *testing.T) {
	var gotQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
	}))
	defer upstream.Close()

	server := mustServer(t, &Config{
		Routes: []Route{{Path: "/api/*", Target: upstream.URL}},
	})

	req := httptest.NewRequest("GET", "/api/search?q=hello&page=2", nil)
	server.ServeHTTP(httptest.NewRecorder(), req)

	if gotQuery != "q=hello&page=2" {
		t.Errorf("upstream query = %q, want %q", gotQuery, "q=hello&page=2")
	}
}

// TestServeHTTP_Rejections covers the request checks performed before proxying
func TestServeHTTP_Rejections(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	server := mustServer(t, &Config{
		Routes: []Route{
			{Path: "/strict", Target: upstream.URL, Methods: []string{"POST"}},
			{
				Path:            "/protected",
				Target:          upstream.URL,
				RequiredHeaders: []string{"X-Company-ID", "X-Integration-ID"},
			},
		},
	})

	tests := []struct {
		name       string
		method     string
		path       string
		headers    map[string]string
		wantStatus int
	}{
		{
			name:       "unknown route",
			method:     "GET",
			path:       "/unknown",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "method not allowed",
			method:     "GET",
			path:       "/strict",
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "allowed method",
			method:     "POST",
			path:       "/strict",
			wantStatus: http.StatusOK,
		},
		{
			name:   "all required headers present",
			method: "GET",
			path:   "/protected",
			headers: map[string]string{
				"X-Company-ID":     "123",
				"X-Integration-ID": "456",
			},
			wantStatus: http.StatusOK,
		},
		{
			name:       "missing one required header",
			method:     "GET",
			path:       "/protected",
			headers:    map[string]string{"X-Company-ID": "123"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing all required headers",
			method:     "GET",
			path:       "/protected",
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			w := httptest.NewRecorder()

			server.ServeHTTP(w, req)

			if got := w.Result().StatusCode; got != tt.wantStatus {
				t.Errorf("status = %d, want %d", got, tt.wantStatus)
			}
		})
	}
}

// TestServeHTTP_BadGateway verifies the error handler when the upstream is down
func TestServeHTTP_BadGateway(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	target := upstream.URL
	upstream.Close() // nothing is listening on target any more

	server := mustServer(t, &Config{
		Server: ServerConfig{Timeout: 2},
		Routes: []Route{{Path: "/down", Target: target}},
	})

	w := httptest.NewRecorder()
	server.ServeHTTP(w, httptest.NewRequest("GET", "/down", nil))

	if got := w.Result().StatusCode; got != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", got)
	}
}

// TestConfig verifies that the accessor hands back a copy of the routes
func TestConfig(t *testing.T) {
	server := mustServer(t, &Config{
		Server: ServerConfig{Port: 1234, Timeout: 7},
		Routes: []Route{{Path: "/a", Target: "http://a.example.com"}},
	})

	config := server.Config()
	if config.Server.Port != 1234 || config.Server.Timeout != 7 {
		t.Errorf("server config = %+v, want port 1234 and timeout 7", config.Server)
	}

	config.Routes[0].Path = "/mutated"
	if server.Config().Routes[0].Path != "/a" {
		t.Error("mutating the returned routes changed the server configuration")
	}
}

// TestStartAndShutdown exercises the real listener and the graceful stop path
func TestStartAndShutdown(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello"))
	}))
	defer upstream.Close()

	port := freePort(t)
	server := mustServer(t, &Config{
		Server: ServerConfig{Port: port, Timeout: 5},
		Routes: []Route{{Path: "/api/*", Target: upstream.URL}},
	})

	errCh := make(chan error, 1)
	go func() { errCh <- server.Start() }()

	url := fmt.Sprintf("http://127.0.0.1:%d/api/thing", port)
	body := waitForOK(t, http.DefaultClient, url)
	if body != "hello" {
		t.Errorf("body = %q, want %q", body, "hello")
	}

	shutdownAndWait(t, server, errCh)
}

// TestStartTLS exercises the TLS listener with a self-signed certificate
func TestStartTLS(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("secure"))
	}))
	defer upstream.Close()

	certFile, keyFile, pool := selfSignedCert(t)

	port := freePort(t)
	server := mustServer(t, &Config{
		Server: ServerConfig{Port: port, Timeout: 5},
		Routes: []Route{{Path: "/api/*", Target: upstream.URL}},
	})

	errCh := make(chan error, 1)
	go func() { errCh <- server.StartTLS(certFile, keyFile) }()

	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
	}
	url := fmt.Sprintf("https://localhost:%d/api/thing", port)
	if body := waitForOK(t, client, url); body != "secure" {
		t.Errorf("body = %q, want %q", body, "secure")
	}

	shutdownAndWait(t, server, errCh)
}

func mustServer(t *testing.T, config *Config) *Server {
	t.Helper()
	server, err := NewServer(config)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	return server
}

// freePort reserves a port and releases it for the server under test to bind.
func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

// waitForOK polls url until the server under test is listening, then returns
// the response body.
func waitForOK(t *testing.T, client *http.Client, url string) string {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := client.Get(url)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			return string(body)
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never became reachable: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func shutdownAndWait(t *testing.T, server *Server, errCh <-chan error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Start() error = %v, want nil after a graceful shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start() did not return after Shutdown()")
	}
}

// selfSignedCert writes a throwaway certificate for the TLS test and returns
// the file paths plus a pool that trusts it.
func selfSignedCert(t *testing.T) (certFile, keyFile string, pool *x509.CertPool) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	dir := t.TempDir()
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	writeFile(t, certFile, string(certPEM))
	writeFile(t, keyFile, string(keyPEM))

	pool = x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatal("failed to add the test certificate to the pool")
	}
	return certFile, keyFile, pool
}
