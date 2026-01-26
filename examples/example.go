package examples

import (
	"log"

	proxy "github.com/josuedeavila/simple-proxy"
)

// New creates a new example server with the given configuration data.
func New(configData []byte) {
	log.Println("Loading configuration from embedded yaml file...")

	config, err := proxy.LoadConfigFromBytes(configData)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Create and start the server
	server := proxy.NewServer(config)

	// Enable standard logging for this example
	server.SetLogger(nil)

	if err := server.Start(); err != nil {
		log.Fatalf("Server stopped with error: %v", err)
	}
}
