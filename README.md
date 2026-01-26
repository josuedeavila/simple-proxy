# Simple Proxy

A configurable HTTP proxy server written in Go with YAML-based routing.

## Features

- **Flexible Routing**: Supports exact path matching and prefix/wildcard routing.
- **Header Manipulation**: Add custom headers, remove headers, or transform headers into query parameters.
- **Method Filtering**: Restrict routes to specific HTTP methods.
- **Configurable**: Simple YAML configuration for server settings and routes.

## Getting Started

### Prerequisites

- Go 1.21 or higher

### Installation

```bash
go get github.com/josuedeavila/simple-proxy
```

## Running the Proxy

You can run the proxy server by providing a configuration file. An example runner is included in the project:

```bash
go run examples/run_proxy.go -config examples/basic.yaml
```

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

```yaml
routes:
  - path: /api/*
    target: https://api.backend.com
    # Request to /api/users/1 -> https://api.backend.com/users/1
```

#### Header Rules

Transform headers before they reach the target:

```yaml
routes:
  - path: /secure
    target: https://secure.backend.com
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

	// Create server
	server := proxy.NewServer(*config)

	// Enable logging (silent by default)
	server.SetLogger(&proxy.DefaultLogger{})

	// Start server
	if err := server.Start(); err != nil {
		log.Fatal(err)
	}
}
```

## License

This project is licensed under the MIT License.
