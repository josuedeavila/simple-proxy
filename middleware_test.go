package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestMiddleware_Execution verifies that a middleware is executed
func TestMiddleware_Execution(t *testing.T) {
	config := &Config{
		Routes: []Route{
			{Path: "/test", Target: "http://example.com"},
		},
	}
	server := NewServer(config)

	executed := false
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			executed = true
			next.ServeHTTP(w, r)
		})
	}

	server.Use(mw)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if !executed {
		t.Error("Middleware was not executed")
	}
}

// TestMiddleware_Order verifies the order of middleware execution
func TestMiddleware_Order(t *testing.T) {
	config := &Config{
		Routes: []Route{
			{Path: "/test", Target: "http://example.com"},
		},
	}
	server := NewServer(config)

	var executionOrder []string

	mw1 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			executionOrder = append(executionOrder, "mw1 start")
			next.ServeHTTP(w, r)
			executionOrder = append(executionOrder, "mw1 end")
		})
	}

	mw2 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			executionOrder = append(executionOrder, "mw2 start")
			next.ServeHTTP(w, r)
			executionOrder = append(executionOrder, "mw2 end")
		})
	}

	// Should execute mw1 -> mw2 -> handler -> mw2 -> mw1
	server.Use(mw1, mw2)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	expected := []string{"mw1 start", "mw2 start", "mw2 end", "mw1 end"}

	if len(executionOrder) != 4 {
		t.Fatalf("Expected 4 steps, got %d: %v", len(executionOrder), executionOrder)
	}

	for i, v := range expected {
		if executionOrder[i] != v {
			t.Errorf("Step %d: expected %s, got %s", i, v, executionOrder[i])
		}
	}
}

// TestMiddleware_Modification verifies that middleware can modify response
func TestMiddleware_Modification(t *testing.T) {
	config := &Config{
		Routes: []Route{
			{Path: "/test", Target: "http://example.com"},
		},
	}
	server := NewServer(config)

	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Middleware-Response", "true")
			next.ServeHTTP(w, r)
		})
	}

	server.Use(mw)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Header().Get("X-Middleware-Response") != "true" {
		t.Error("Middleware failed to set response header")
	}
}

// TestMiddleware_ShortCircuit verifies that middleware can stop the chain
func TestMiddleware_ShortCircuit(t *testing.T) {
	config := &Config{
		Routes: []Route{
			{Path: "/test", Target: "http://example.com"},
		},
	}
	server := NewServer(config)

	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte("Forbidden by middleware"))
		})
	}

	server.Use(mw)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusForbidden {
		t.Errorf("Expected status 403, got %d", w.Result().StatusCode)
	}

	// Should not hit proxy error or anything else because chain stopped
	// We didn't set up a valid target so if it continued it would likely 502 or 404 or something,
	// but we just check the status code we set.
}
