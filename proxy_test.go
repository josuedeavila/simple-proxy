package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestLoadConfigFromBytes tests loading configuration from a byte slice
func TestLoadConfigFromBytes(t *testing.T) {
	yamlData := []byte(`
server:
  port: 9090
  timeout: 60
routes:
  - path: /test
    target: http://example.com
    methods: [GET]
`)

	config, err := LoadConfigFromBytes(yamlData)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	if config.Server.Port != 9090 {
		t.Errorf("Expected port 9090, got %d", config.Server.Port)
	}
	if config.Server.Timeout != 60 {
		t.Errorf("Expected timeout 60, got %d", config.Server.Timeout)
	}
	if len(config.Routes) != 1 {
		t.Errorf("Expected 1 route, got %d", len(config.Routes))
	}
	if config.Routes[0].Path != "/test" {
		t.Errorf("Expected route path /test, got %s", config.Routes[0].Path)
	}
}

// TestApplyDefaults tests if default values are applied correctly
func TestApplyDefaults(t *testing.T) {
	yamlData := []byte(`
routes:
  - path: /test
    target: http://example.com
`)

	config, err := LoadConfigFromBytes(yamlData)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	if config.Server.Port != 8000 {
		t.Errorf("Expected default port 8000, got %d", config.Server.Port)
	}
	if config.Server.Timeout != 30 {
		t.Errorf("Expected default timeout 30, got %d", config.Server.Timeout)
	}
}

// TestLoadConfigFromDir tests loading configuration from a directory
func TestLoadConfigFromDir(t *testing.T) {
	// Create a temporary directory
	dir, err := os.MkdirTemp("", "proxy_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// Create first config file
	config1 := []byte(`
server:
  port: 9091
routes:
  - path: /route1
    target: http://target1.com
`)
	if err := os.WriteFile(filepath.Join(dir, "config1.yaml"), config1, 0644); err != nil {
		t.Fatal(err)
	}

	// Create second config file
	config2 := []byte(`
routes:
  - path: /route2
    target: http://target2.com
`)
	if err := os.WriteFile(filepath.Join(dir, "config2.yml"), config2, 0644); err != nil {
		t.Fatal(err)
	}

	config, err := LoadConfigFromDir(dir)
	if err != nil {
		t.Fatalf("Failed to load config from dir: %v", err)
	}

	if config.Server.Port != 9091 {
		t.Errorf("Expected port 9091, got %d", config.Server.Port)
	}
	if len(config.Routes) != 2 {
		t.Errorf("Expected 2 routes, got %d", len(config.Routes))
	}
}

// TestPathMatches tests the logic for route matching
func TestPathMatches(t *testing.T) {
	server := &Server{}

	tests := []struct {
		requestPath string
		routePath   string
		expected    bool
	}{
		{"/exact", "/exact", true},
		{"/exact/extra", "/exact", false},
		{"/prefix/resource", "/prefix/*", true},
		{"/prefix", "/prefix/*", true},
		{"/other", "/exact", false},
	}

	for _, tt := range tests {
		result := server.pathMatches(tt.requestPath, tt.routePath)
		if result != tt.expected {
			t.Errorf("pathMatches(%q, %q) = %v, expected %v", tt.requestPath, tt.routePath, result, tt.expected)
		}
	}
}

// TestMethodAllowed tests the HTTP method checking logic
func TestMethodAllowed(t *testing.T) {
	server := &Server{}
	allowed := []string{"GET", "POST"}

	if !server.methodAllowed("GET", allowed) {
		t.Error("GET should be allowed")
	}
	if !server.methodAllowed("post", allowed) { // Case insensitive
		t.Error("post should be allowed")
	}
	if server.methodAllowed("DELETE", allowed) {
		t.Error("DELETE should not be allowed")
	}
}

// TestServeHTTP_Proxying tests the full proxying flow including modifications
func TestServeHTTP_Proxying(t *testing.T) {
	// 1. Create a mock upstream server
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify modifications

		// Check Path
		if r.URL.Path != "/upstream/resource" {
			t.Errorf("Upstream received path %s, expected /upstream/resource", r.URL.Path)
		}

		// Check Query Param Transformation (From X-User-ID header)
		if q := r.URL.Query().Get("user"); q != "123" {
			t.Errorf("Expected query param user=123, got %s", q)
		}

		// Check Header Removal
		if r.Header.Get("X-User-ID") != "" {
			t.Error("Expected X-User-ID header to be removed")
		}

		// Check Added Header
		if r.Header.Get("X-Proxy-Add") != "added" {
			t.Errorf("Expected X-Proxy-Add header 'added', got %s", r.Header.Get("X-Proxy-Add"))
		}

		// Send response
		w.Header().Set("X-Upstream-Response", "ok")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("response from upstream"))
	}))
	defer mockUpstream.Close()

	// 2. Configure Proxy Server
	config := &Config{
		Server: ServerConfig{Port: 0, Timeout: 5},
		Routes: []Route{
			{
				Path:    "/api/*",
				Target:  mockUpstream.URL + "/upstream",
				Methods: []string{"GET"},
				AddHeaders: map[string]string{
					"X-Proxy-Add": "added",
				},
				HeaderRules: []HeaderRule{
					{
						FromHeader: "X-User-ID",
						ToQuery:    "user",
						Remove:     true,
					},
				},
			},
		},
	}

	proxyServer := NewServer(config)

	// 3. Create Request to Proxy
	req := httptest.NewRequest("GET", "/api/resource", nil)
	req.Header.Set("X-User-ID", "123")
	w := httptest.NewRecorder()

	// 4. Serve
	proxyServer.ServeHTTP(w, req)

	// 5. Check Proxy Response
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "response from upstream" {
		t.Errorf("Expected body 'response from upstream', got %s", string(body))
	}

	if resp.Header.Get("X-Upstream-Response") != "ok" {
		t.Error("Expected X-Upstream-Response header")
	}
}

// TestServeHTTP_NotFound tests 404 behavior
func TestServeHTTP_NotFound(t *testing.T) {
	proxyServer := NewServer(&Config{})
	req := httptest.NewRequest("GET", "/unknown", nil)
	w := httptest.NewRecorder()

	proxyServer.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusNotFound {
		t.Errorf("Expected 404, got %d", w.Result().StatusCode)
	}
}

// TestServeHTTP_MethodNotAllowed tests 405 behavior
func TestServeHTTP_MethodNotAllowed(t *testing.T) {
	config := &Config{
		Routes: []Route{
			{
				Path:    "/strict",
				Target:  "http://example.com",
				Methods: []string{"POST"},
			},
		},
	}
	proxyServer := NewServer(config)

	req := httptest.NewRequest("GET", "/strict", nil)
	w := httptest.NewRecorder()

	proxyServer.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("Expected 405, got %d", w.Result().StatusCode)
	}
}

// TestServeHTTP_RequiredHeaders tests handling of RequiredHeaders route config
func TestServeHTTP_RequiredHeaders(t *testing.T) {
	config := &Config{
		Routes: []Route{
			{
				Path:    "/protected",
				Target:  "http://example.com",
				RequiredHeaders: []string{"X-Company-ID", "X-Integration-ID"},
			},
		},
	}
	proxyServer := NewServer(config)

	// Create a mock upstream server to test a successful 200 OK request
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mockUpstream.Close()
	
	config.Routes[0].Target = mockUpstream.URL

	proxyServer = NewServer(config)

	tests := []struct {
		name           string
		headers        map[string]string
		expectedStatus int
	}{
		{
			name: "All required headers present",
			headers: map[string]string{
				"X-Company-ID":     "123",
				"X-Integration-ID": "456",
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "Missing one required header",
			headers: map[string]string{
				"X-Company-ID": "123",
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Missing all required headers",
			headers:        map[string]string{},
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/protected", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			w := httptest.NewRecorder()

			proxyServer.ServeHTTP(w, req)

			if w.Result().StatusCode != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, w.Result().StatusCode)
			}
		})
	}
}
