package main

import (
	_ "embed"
	"log"
	"net/http"
	"time"

	proxy "github.com/josuedeavila/simple-proxy"
)

//go:embed middleware.yaml
var configData []byte

// StdoutLogger implements the proxy.Logger interface
type StdoutLogger struct{}

func (l *StdoutLogger) Printf(format string, v ...any) {
	log.Printf("[ProxyInternal] "+format, v...)
}

// LoggingMiddleware logs the request method, path and duration
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		log.Printf("[Middleware] Started %s %s", r.Method, r.URL.Path)

		next.ServeHTTP(w, r)

		log.Printf("[Middleware] Completed %s %s in %v", r.Method, r.URL.Path, time.Since(start))
	})
}

// HeaderMiddleware adds a custom header to the response
func HeaderMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Println("[Middleware] Adding X-Example-Middleware header")
		w.Header().Set("X-Example-Middleware", "true")
		next.ServeHTTP(w, r)
	})
}

// AuthMiddleware simulates a simple authentication check
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// In a real scenario, check for a valid token or session
		log.Println("[Middleware] Checking authentication (pass-through for demo)")
		next.ServeHTTP(w, r)
	})
}

func main() {
	// Load configuration
	config, err := proxy.LoadConfigFromBytes(configData)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Create server
	server, err := proxy.NewServer(config)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	// Set a basic logger for the proxy internals
	server.SetLogger(&StdoutLogger{})

	// Register Middleware
	// Order matters: Logging wraps Header, which wraps Auth, which wraps the Proxy.
	server.Use(LoggingMiddleware, HeaderMiddleware, AuthMiddleware)

	log.Printf("Starting server on port %d with middleware enabled...", config.Server.Port)
	log.Println("Try: curl -v http://localhost:8083/uuid")

	if err := server.Start(); err != nil {
		log.Fatal(err)
	}
}
