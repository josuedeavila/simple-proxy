// Package proxy provides a configurable HTTP proxy server with YAML-based routing
package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v3"
)

// Config represents the main configuration
type Config struct {
	Server ServerConfig `yaml:"server" json:"server"`
	Routes []Route      `yaml:"routes" json:"routes"`
}

// ServerConfig contains server settings
type ServerConfig struct {
	Port    int `yaml:"port" json:"port"`
	Timeout int `yaml:"timeout" json:"timeout"` // in seconds
}

// Route represents a proxy route configuration
type Route struct {
	Path            string            `yaml:"path" json:"path"`
	Target          string            `yaml:"target" json:"target"`
	Methods         []string          `yaml:"methods" json:"methods"`
	HeaderRules     []HeaderRule      `yaml:"header_rules" json:"header_rules"`
	AddHeaders      map[string]string `yaml:"add_headers" json:"add_headers"`
	RequiredHeaders []string          `yaml:"required_headers" json:"required_headers"`
}

// HeaderRule defines how to transform a header
type HeaderRule struct {
	FromHeader string `yaml:"from_header" json:"from_header"`
	ToQuery    string `yaml:"to_query" json:"to_query"`
	Remove     bool   `yaml:"remove" json:"remove"` // Remove header after transformation
}

// Middleware defines a function to process request
type Middleware func(http.Handler) http.Handler

// Logger interface for custom logging
type Logger interface {
	Printf(format string, v ...any)
}

// DefaultLogger forwards proxy activity to the standard library logger.
type DefaultLogger struct{}

// Printf implements Logger.
func (DefaultLogger) Printf(format string, v ...any) { log.Printf(format, v...) }

type nopLogger struct{}

func (nopLogger) Printf(string, ...any) {}

// routeEntry pairs a configured route with the reverse proxy that serves it.
type routeEntry struct {
	route *Route
	proxy *httputil.ReverseProxy
}

// Server represents the proxy server
type Server struct {
	config     *Config
	routes     []routeEntry
	httpServer *http.Server

	mu          sync.Mutex // guards middlewares
	middlewares []Middleware

	logger  atomic.Pointer[Logger]
	handler atomic.Pointer[http.Handler]
}

// LoadConfigFromDir loads every YAML file in a directory, in lexical order.
// Routes are concatenated; server settings come from the first file that
// defines them. Any unreadable or invalid file is reported as an error.
func LoadConfigFromDir(dir string) (*Config, error) {
	files, err := yamlFiles(dir)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("proxy: no YAML files found in directory: %s", dir)
	}

	configs := make([]*Config, 0, len(files))
	for _, file := range files {
		config, err := loadFile(file)
		if err != nil {
			return nil, fmt.Errorf("proxy: %s: %w", file, err)
		}
		configs = append(configs, config)
	}

	return mergeConfigs(configs), nil
}

// yamlFiles returns the .yaml and .yml files in dir, sorted by name.
func yamlFiles(dir string) ([]string, error) {
	var files []string
	for _, pattern := range []string{"*.yaml", "*.yml"} {
		matches, err := filepath.Glob(filepath.Join(dir, pattern))
		if err != nil {
			return nil, err
		}
		files = append(files, matches...)
	}
	sort.Strings(files)
	return files, nil
}

// LoadConfigFromFile loads a single YAML file
func LoadConfigFromFile(path string) (*Config, error) {
	config, err := loadFile(path)
	if err != nil {
		return nil, err
	}
	return applyDefaults(config), nil
}

// LoadConfigFromBytes loads configuration from byte slice
func LoadConfigFromBytes(data []byte) (*Config, error) {
	config, err := parseConfig(data)
	if err != nil {
		return nil, err
	}
	return applyDefaults(config), nil
}

// LoadConfigFromJSONString loads configuration from a JSON string.
func LoadConfigFromJSONString(data string) (*Config, error) {
	if strings.TrimSpace(data) == "" {
		return nil, errors.New("proxy: empty JSON configuration")
	}

	var config Config
	if err := json.Unmarshal([]byte(data), &config); err != nil {
		return nil, fmt.Errorf("proxy: invalid JSON configuration: %w", err)
	}
	return applyDefaults(&config), nil
}

// LoadConfigFromEnv loads configuration from a JSON string held by the named
// environment variable, which must be set and non-empty.
func LoadConfigFromEnv(name string) (*Config, error) {
	if name == "" {
		return nil, errors.New("proxy: environment variable name must not be empty")
	}

	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return nil, fmt.Errorf("proxy: environment variable %s is not set or empty", name)
	}

	config, err := LoadConfigFromJSONString(value)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return config, nil
}

func loadFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseConfig(data)
}

func parseConfig(data []byte) (*Config, error) {
	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	return &config, nil
}

// mergeConfigs combines multiple configs into one without mutating its inputs.
func mergeConfigs(configs []*Config) *Config {
	merged := &Config{}
	for _, config := range configs {
		if merged.Server.Port == 0 {
			merged.Server.Port = config.Server.Port
		}
		if merged.Server.Timeout == 0 {
			merged.Server.Timeout = config.Server.Timeout
		}
		merged.Routes = append(merged.Routes, config.Routes...)
	}
	return applyDefaults(merged)
}

// applyDefaults sets default values if not specified
func applyDefaults(config *Config) *Config {
	if config.Server.Port == 0 {
		config.Server.Port = 8000
	}
	if config.Server.Timeout == 0 {
		config.Server.Timeout = 30
	}
	return config
}

// NewServer creates a new proxy server with the given configuration.
// It fails if any route is missing a path or points at an unusable target,
// so a broken route is reported at startup rather than as a 500 at runtime.
func NewServer(config *Config) (*Server, error) {
	if config == nil {
		return nil, errors.New("proxy: config must not be nil")
	}
	applyDefaults(config)

	s := &Server{config: config}
	s.SetLogger(nil)

	timeout := time.Duration(config.Server.Timeout) * time.Second
	transport := newTransport(timeout)

	for i := range config.Routes {
		entry, err := s.newRouteEntry(&config.Routes[i], transport)
		if err != nil {
			return nil, err
		}
		s.routes = append(s.routes, entry)
	}
	sortRoutes(s.routes)

	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", config.Server.Port),
		Handler: s,
		// ReadTimeout and WriteTimeout are deliberately left unset: a proxy
		// must be able to stream large uploads and downloads. ReadHeaderTimeout
		// is what closes slow-header (Slowloris) connections.
		ReadHeaderTimeout: timeout,
		IdleTimeout:       2 * timeout,
	}

	s.rebuildHandler()
	return s, nil
}

// newTransport clones the standard transport so routes keep connection
// pooling, dial timeouts, HTTP/2 and proxy-from-environment support.
func newTransport(timeout time.Duration) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = timeout
	return transport
}

// newRouteEntry builds the reverse proxy that serves a single route.
func (s *Server) newRouteEntry(route *Route, transport http.RoundTripper) (routeEntry, error) {
	if route.Path == "" {
		return routeEntry{}, errors.New("proxy: route with empty path")
	}

	target, err := url.Parse(route.Target)
	if err != nil {
		return routeEntry{}, fmt.Errorf("proxy: route %s: invalid target %q: %w", route.Path, route.Target, err)
	}
	if target.Scheme == "" || target.Host == "" {
		return routeEntry{}, fmt.Errorf("proxy: route %s: target %q must include scheme and host", route.Path, route.Target)
	}

	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host
			s.customizeRequest(req, route, target)
			if _, ok := req.Header["User-Agent"]; !ok {
				req.Header.Set("User-Agent", "")
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			s.log().Printf("proxy error for %s: %v", r.URL.Path, err)
			http.Error(w, "Bad Gateway", http.StatusBadGateway)
		},
	}

	return routeEntry{route: route, proxy: proxy}, nil
}

// sortRoutes orders routes from most to least specific, so that an exact path
// is never shadowed by a wildcard and the longest wildcard prefix wins.
// Routes of equal specificity keep their configuration order.
func sortRoutes(routes []routeEntry) {
	sort.SliceStable(routes, func(i, j int) bool {
		a, b := routes[i].route.Path, routes[j].route.Path
		if isWildcard(a) != isWildcard(b) {
			return !isWildcard(a)
		}
		return len(a) > len(b)
	})
}

func isWildcard(routePath string) bool { return strings.HasSuffix(routePath, "/*") }

// customizeRequest modifies the request according to route rules
func (s *Server) customizeRequest(req *http.Request, route *Route, target *url.URL) {
	req.URL.Path = rewritePath(req.URL.Path, route.Path, target.Path)
	req.URL.RawPath = ""

	s.applyHeaderRules(req, route)

	for key, value := range route.AddHeaders {
		req.Header.Set(key, value)
	}
}

// rewritePath maps an incoming path onto the target. A wildcard route strips
// its prefix and appends the remainder to the target path; an exact route
// always maps to the target path.
func rewritePath(requestPath, routePath, targetPath string) string {
	if !isWildcard(routePath) {
		return orRoot(targetPath)
	}

	prefix := strings.TrimSuffix(routePath, "/*")
	rest := strings.TrimPrefix(requestPath, prefix)
	if rest == "" {
		return orRoot(targetPath)
	}
	return strings.TrimSuffix(targetPath, "/") + rest
}

func orRoot(path string) string {
	if path == "" {
		return "/"
	}
	return path
}

// applyHeaderRules copies configured headers into the target query string.
// Header values are never logged: they are commonly credentials.
func (s *Server) applyHeaderRules(req *http.Request, route *Route) {
	if len(route.HeaderRules) == 0 {
		return
	}

	query := req.URL.Query()
	for _, rule := range route.HeaderRules {
		value := req.Header.Get(rule.FromHeader)
		if value == "" {
			continue
		}
		query.Set(rule.ToQuery, value)
		if rule.Remove {
			req.Header.Del(rule.FromHeader)
		}
		s.log().Printf("transformed header %s into query param %s", rule.FromHeader, rule.ToQuery)
	}
	req.URL.RawQuery = query.Encode()
}

// SetLogger sets a custom logger. A nil logger disables logging.
// It is safe to call while the server is running.
func (s *Server) SetLogger(logger Logger) {
	if logger == nil {
		logger = nopLogger{}
	}
	s.logger.Store(&logger)
}

func (s *Server) log() Logger { return *s.logger.Load() }

// Use adds middleware to the server, outermost first. It is safe to call while
// the server is running; in-flight requests keep the chain they started with.
func (s *Server) Use(mw ...Middleware) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.middlewares = append(s.middlewares, mw...)
	s.rebuildHandler()
}

// rebuildHandler constructs the final handler chain. Callers must hold s.mu,
// except in NewServer where the server is not yet shared.
func (s *Server) rebuildHandler() {
	var handler http.Handler = http.HandlerFunc(s.handleRequest)

	// Apply middlewares in reverse order
	for i := len(s.middlewares) - 1; i >= 0; i-- {
		handler = s.middlewares[i](handler)
	}
	s.handler.Store(&handler)
}

// Config returns the server configuration. The routes slice is copied, but the
// maps and slices inside each route are shared with the server.
func (s *Server) Config() Config {
	config := *s.config
	config.Routes = append([]Route(nil), s.config.Routes...)
	return config
}

// ServeHTTP implements http.Handler and delegates to the final handler chain
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	(*s.handler.Load()).ServeHTTP(w, r)
}

// handleRequest performs the actual proxying logic
func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	entry := s.findRoute(r.URL.Path)
	if entry == nil {
		http.NotFound(w, r)
		return
	}
	route := entry.route

	if len(route.Methods) > 0 && !methodAllowed(r.Method, route.Methods) {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if missing := missingHeader(r, route.RequiredHeaders); missing != "" {
		s.log().Printf("missing required header %s for %s", missing, r.URL.Path)
		http.Error(w, "Missing required header: "+missing, http.StatusBadRequest)
		return
	}

	s.log().Printf("%s %s -> %s", r.Method, r.URL.Path, route.Target)
	entry.proxy.ServeHTTP(w, r)
}

// Start starts the proxy server and blocks until it stops.
// It returns nil after a graceful Shutdown.
func (s *Server) Start() error {
	s.logStartup("")
	return ignoreServerClosed(s.httpServer.ListenAndServe())
}

// StartTLS starts the proxy server with TLS and blocks until it stops.
// It returns nil after a graceful Shutdown.
func (s *Server) StartTLS(certFile, keyFile string) error {
	s.logStartup(" (TLS)")
	return ignoreServerClosed(s.httpServer.ListenAndServeTLS(certFile, keyFile))
}

// Shutdown gracefully shuts the server down without interrupting active
// requests, and makes Start return.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func ignoreServerClosed(err error) error {
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) logStartup(suffix string) {
	logger := s.log()
	logger.Printf("proxy server starting on %s%s", s.httpServer.Addr, suffix)
	logger.Printf("loaded %d routes", len(s.routes))
	for i := range s.routes {
		route := s.routes[i].route
		logger.Printf("  %s -> %s (methods: %v)", route.Path, route.Target, route.Methods)
	}
}

// findRoute returns the most specific route matching the request path.
func (s *Server) findRoute(requestPath string) *routeEntry {
	for i := range s.routes {
		if pathMatches(requestPath, s.routes[i].route.Path) {
			return &s.routes[i]
		}
	}
	return nil
}

// pathMatches reports whether a request path matches a route path. A wildcard
// route matches its prefix and everything below it, but not a longer path
// segment that merely starts with the same characters.
func pathMatches(requestPath, routePath string) bool {
	if requestPath == routePath {
		return true
	}
	if !isWildcard(routePath) {
		return false
	}

	prefix := strings.TrimSuffix(routePath, "/*")
	return requestPath == prefix || strings.HasPrefix(requestPath, prefix+"/")
}

// methodAllowed checks if the HTTP method is allowed
func methodAllowed(method string, allowed []string) bool {
	for _, m := range allowed {
		if strings.EqualFold(m, method) {
			return true
		}
	}
	return false
}

// missingHeader returns the first required header absent from the request.
func missingHeader(r *http.Request, required []string) string {
	for _, header := range required {
		if r.Header.Get(header) == "" {
			return header
		}
	}
	return ""
}
