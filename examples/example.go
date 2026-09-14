package examples

import (
	"log"
	"os"

	proxy "github.com/josuedeavila/simple-proxy"
)

// ConfigEnvVar holds the JSON configuration used by every example when set.
const ConfigEnvVar = "PROXY_CONFIG"

// New creates a new example server with the given configuration data.
// PROXY_CONFIG, when set, takes precedence over the embedded yaml file.
func New(configData []byte) {
	config, err := LoadConfig(configData)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Create and start the server
	server, err := proxy.NewServer(config)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	// Enable standard logging for this example
	server.SetLogger(proxy.DefaultLogger{})

	if err := server.Start(); err != nil {
		log.Fatalf("Server stopped with error: %v", err)
	}
}

// LoadConfig reads the configuration from the PROXY_CONFIG environment
// variable when it is set, and falls back to the embedded yaml file.
func LoadConfig(configData []byte) (*proxy.Config, error) {
	if os.Getenv(ConfigEnvVar) != "" {
		log.Printf("Loading configuration from the %s environment variable...", ConfigEnvVar)
		return proxy.LoadConfigFromEnv(ConfigEnvVar)
	}

	log.Println("Loading configuration from embedded yaml file...")
	return proxy.LoadConfigFromBytes(configData)
}
