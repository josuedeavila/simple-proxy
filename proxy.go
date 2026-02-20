// Package proxy provides a configurable HTTP proxy server with YAML-based routing
package proxy

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config represents the main configuration
type Config struct {
	Server ServerConfig `yaml:"server"`
	Routes []Route      `yaml:"routes"`
}

// ServerConfig contains server settings
type ServerConfig struct {
	Port    int `yaml:"port"`
	Timeout int `yaml:"timeout"` // in seconds
}

// Route represents a proxy route configuration
type Route struct {
	Path        string            `yaml:"path"`
	Target      string            `yaml:"target"`
	Methods     []string          `yaml:"methods"`
	HeaderRules []HeaderRule      `yaml:"header_rules"`
	AddHeaders  map[string]string `yaml:"add_headers"`
}

// HeaderRule defines how to transform a header
type HeaderRule struct {
	FromHeader string `yaml:"from_header"`
	ToQuery    string `yaml:"to_query"`
	Remove     bool   `yaml:"remove"` // Remove header after transformation
}

// Middleware defines a function to process request
type Middleware func(http.Handler) http.Handler

// Server represents the proxy server
type Server struct {
	config       *Config
	proxies      map[string]*httputil.ReverseProxy
	logger       Logger
	middlewares  []Middleware
	finalHandler http.Handler
}

// Logger interface for custom logging
type Logger interface {
	Printf(format string, v ...any)
}

type nopLogger struct{}

func (n *nopLogger) Printf(format string, v ...any) {}

// LoadConfigFromDir loads all YAML files from a directory
func LoadConfigFromDir(dir string) (*Config, error) {
	var configs []*Config

	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}

	ymlFiles, err := filepath.Glob(filepath.Join(dir, "*.yml"))
	if err != nil {
		return nil, err
	}
	files = append(files, ymlFiles...)

	if len(files) == 0 {
		return nil, fmt.Errorf("no YAML files found in directory: %s", dir)
	}

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			log.Printf("warning: failed to read %s: %v", file, err)
			continue
		}

		var config Config
		if err := yaml.Unmarshal(data, &config); err != nil {
			log.Printf("warning: failed to parse %s: %v", file, err)
			continue
		}

		configs = append(configs, &config)
		log.Printf("loaded config from: %s", file)
	}

	if len(configs) == 0 {
		return nil, fmt.Errorf("no valid configuration files found")
	}

	return mergeConfigs(configs), nil
}

// LoadConfigFromFile loads a single YAML file
func LoadConfigFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return applyDefaults(&config), nil
}

// LoadConfigFromBytes loads configuration from byte slice
func LoadConfigFromBytes(data []byte) (*Config, error) {
	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return applyDefaults(&config), nil
}

// mergeConfigs combines multiple configs into one
func mergeConfigs(configs []*Config) *Config {
	if len(configs) == 0 {
		return nil
	}

	merged := configs[0]

	// Append routes from other configs
	for i := 1; i < len(configs); i++ {
		merged.Routes = append(merged.Routes, configs[i].Routes...)
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

// NewServer creates a new proxy server with the given configuration
func NewServer(config *Config) *Server {
	s := &Server{
		config:      config,
		proxies:     make(map[string]*httputil.ReverseProxy),
		logger:      &nopLogger{},
		middlewares: []Middleware{},
	}

	// Create reverse proxies for each route
	for i := range config.Routes {
		route := &config.Routes[i]
		targetURL, err := url.Parse(route.Target)
		if err != nil {
			log.Printf("warning: invalid target URL for route %s: %v", route.Path, err)
			continue
		}

		proxy := httputil.NewSingleHostReverseProxy(targetURL)

		// Customize the Director to handle header rules and path rewriting
		originalDirector := proxy.Director
		proxy.Director = func(req *http.Request) {
			originalPath := req.URL.Path
			originalDirector(req)
			s.customizeRequest(req, route, targetURL, originalPath)
		}

		// Set timeout on the transport
		proxy.Transport = &http.Transport{
			ResponseHeaderTimeout: time.Duration(config.Server.Timeout) * time.Second,
		}

		// Custom error handler
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			s.logger.Printf("proxy error for %s: %v", r.URL.Path, err)
			http.Error(w, "Bad Gateway", http.StatusBadGateway)
		}

		s.proxies[route.Path] = proxy
	}

	s.rebuildHandler()
	return s
}

// customizeRequest modifies the request according to route rules
func (s *Server) customizeRequest(req *http.Request, route *Route, targetURL *url.URL, originalPath string) {
	// Handle path rewriting for wildcard routes
	if strings.HasSuffix(route.Path, "/*") {
		prefix := strings.TrimSuffix(route.Path, "/*")
		remainingPath := strings.TrimPrefix(originalPath, prefix)
		req.URL.Path = targetURL.Path + remainingPath
	} else {
		req.URL.Path = targetURL.Path
	}

	// Process query parameters from header rules
	query := req.URL.Query()
	for _, rule := range route.HeaderRules {
		if headerValue := req.Header.Get(rule.FromHeader); headerValue != "" {
			query.Set(rule.ToQuery, headerValue)
			s.logger.Printf("Transformed %s: %s -> query param %s", rule.FromHeader, headerValue, rule.ToQuery)

			// Remove header if specified
			if rule.Remove {

				req.Header.Del(rule.FromHeader)
			}
		}
	}
	req.URL.RawQuery = query.Encode()

	// Add custom headers
	for key, value := range route.AddHeaders {
		req.Header.Set(key, value)
	}

	// Set the Host header to match the target
	req.Host = targetURL.Host
}

// SetLogger sets a custom logger
func (s *Server) SetLogger(logger Logger) {
	if logger == nil {
		logger = &nopLogger{}
	}
	s.logger = logger
}

// Use adds middleware to the server
func (s *Server) Use(mw ...Middleware) {
	s.middlewares = append(s.middlewares, mw...)
	s.rebuildHandler()
}

// rebuildHandler constructs the final handler chain
func (s *Server) rebuildHandler() {
	var handler http.Handler = http.HandlerFunc(s.handleRequest)

	// Apply middlewares in reverse order
	for i := len(s.middlewares) - 1; i >= 0; i-- {
		handler = s.middlewares[i](handler)
	}
	s.finalHandler = handler
}

// GetConfig returns the server configuration
func (s *Server) GetConfig() *Config {
	return s.config
}

// ServeHTTP implements http.Handler and delegates to the final handler chain
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.finalHandler == nil {
		s.rebuildHandler()
	}
	s.finalHandler.ServeHTTP(w, r)
}

// handleRequest performs the actual proxying logic
func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	// Find matching route
	route := s.findRoute(r)
	if route == nil {
		http.NotFound(w, r)
		return
	}

	// Check method
	if len(route.Methods) > 0 && !s.methodAllowed(r.Method, route.Methods) {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get the proxy for this route and forward
	proxy := s.proxies[route.Path]
	if proxy == nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	s.logger.Printf("%s %s -> %s", r.Method, r.URL.Path, route.Target)
	proxy.ServeHTTP(w, r)
}

// Start starts the proxy server
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.config.Server.Port)
	s.logger.Printf("Proxy server starting on %s", addr)
	s.logger.Printf("Loaded %d routes", len(s.config.Routes))
	for _, route := range s.config.Routes {
		s.logger.Printf("  %s -> %s (methods: %v)", route.Path, route.Target, route.Methods)
	}

	return http.ListenAndServe(addr, s)
}

// StartTLS starts the proxy server with TLS
func (s *Server) StartTLS(certFile, keyFile string) error {
	addr := fmt.Sprintf(":%d", s.config.Server.Port)
	s.logger.Printf("Proxy server starting on %s (TLS)", addr)
	s.logger.Printf("Loaded %d routes", len(s.config.Routes))

	return http.ListenAndServeTLS(addr, certFile, keyFile, s)
}

// findRoute finds the matching route for a request
func (s *Server) findRoute(r *http.Request) *Route {
	for i := range s.config.Routes {
		route := &s.config.Routes[i]
		if s.pathMatches(r.URL.Path, route.Path) {
			return route
		}
	}
	return nil
}

// pathMatches checks if a request path matches a route path
func (s *Server) pathMatches(requestPath, routePath string) bool {
	if requestPath == routePath {
		return true
	}

	// Prefix match (if route path ends with /*)
	if strings.HasSuffix(routePath, "/*") {
		prefix := strings.TrimSuffix(routePath, "/*")
		return strings.HasPrefix(requestPath, prefix)
	}

	return false
}

// methodAllowed checks if the HTTP method is allowed
func (s *Server) methodAllowed(method string, allowed []string) bool {
	for _, m := range allowed {
		if strings.EqualFold(m, method) {
			return true
		}
	}
	return false
}
