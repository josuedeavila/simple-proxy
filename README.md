# Simple Proxy

A configurable HTTP proxy server written in Go with YAML-based routing.

## Features

- **Flexible Routing**: Supports exact path matching and prefix/wildcard routing.
- **Header Manipulation**: Add custom headers, remove headers, or transform headers into query parameters.
- **Method Filtering**: Restrict routes to specific HTTP methods.
- **Header Validation**: Require specific headers to be present before routing.
- **Configurable**: Simple YAML configuration for server settings and routes.

## Getting Started

### Prerequisites

- Go 1.26 or higher

### Installation

```bash
go get github.com/josuedeavila/simple-proxy
```

## Running the Proxy

You can run the proxy server by providing a configuration file. An example runner is included in the project:

```bash
go run ./examples/basic
```

Each directory under `examples/` embeds its own YAML file: `basic`, `prefix`,
`headers` and `middleware`.

## Configuration

The configuration is defined in YAML format.

### Basic Example

```yaml
server:
  port: 8080
  timeout: 30

routes:
  - path: /google
    target: https://www.google.com
    methods:
      - GET
```

### Advanced Routing

#### Wildcards

Use `/*` to match all paths under a prefix. The prefix is stripped and appended to the target.
A wildcard only matches whole path segments, so `/api/*` matches `/api` and `/api/users` but not `/apifoo`.

Routes are matched from most to least specific: an exact path always wins over a
wildcard, and the longest wildcard prefix wins over a shorter one, regardless of
the order they appear in the configuration.

```yaml
routes:
  - path: /api/*
    target: https://api.backend.com
    # Request to /api/users/1 -> https://api.backend.com/users/1
```

#### Header Validation & Rules

Require specific headers to be present before routing, and transform headers before they reach the target:

```yaml
routes:
  - path: /secure
    target: https://secure.backend.com
    required_headers:
      - "X-Company-ID"
    header_rules:
      - from_header: "X-API-Key"
        to_query: "apikey"
        remove: true
```

## Library Usage

You can embed the proxy in your own Go application:

```go
package main

import (
	"log"

	proxy "github.com/josuedeavila/simple-proxy"
)

func main() {
	// Load configuration
	config, err := proxy.LoadConfigFromFile("config.yaml")
	if err != nil {
		log.Fatal(err)
	}

	// Create server. This fails if a route has no path or an unusable target,
	// so a broken configuration is caught here instead of at request time.
	server, err := proxy.NewServer(config)
	if err != nil {
		log.Fatal(err)
	}

	// Enable logging (silent by default)
	server.SetLogger(proxy.DefaultLogger{})

	// Start server. Start blocks and returns nil after a graceful Shutdown.
	if err := server.Start(); err != nil {
		log.Fatal(err)
	}
}
```

### Loading the configuration

| Function                         | Source                                           |
| -------------------------------- | ------------------------------------------------ |
| `LoadConfigFromFile(path)`       | a single YAML file                               |
| `LoadConfigFromDir(dir)`         | every `.yaml`/`.yml` file in a directory, merged |
| `LoadConfigFromBytes(data)`      | YAML held in memory                              |
| `LoadConfigFromJSONString(data)` | JSON held in a string                            |
| `LoadConfigFromEnv(name)`        | JSON held in the named environment variable      |

The JSON and YAML documents use the same keys, so either format describes the
same configuration.

```go
config, err := proxy.LoadConfigFromEnv("PROXY_CONFIG")
```

```bash
PROXY_CONFIG='{"server":{"port":8080},"routes":[{"path":"/api/*","target":"https://jsonplaceholder.typicode.com"}]}' \
	go run ./examples/basic
```

### Middleware

`Use` wraps the proxy with standard `func(http.Handler) http.Handler` middleware,
outermost first:

```go
server.Use(LoggingMiddleware, AuthMiddleware)
```

### Graceful shutdown

```go
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()

if err := server.Shutdown(ctx); err != nil {
	log.Print(err)
}
```

`Shutdown` stops accepting new connections, waits for in-flight requests, and
makes `Start` return `nil`.

## License

This project is licensed under the MIT License.
