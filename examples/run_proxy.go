package main

import (
	"flag"
	"log"
	"os"

	proxy "github.com/josuedeavila/simple-proxy"
)

func main() {
	configPath := flag.String("config", "", "Path to configuration file or directory")
	flag.Parse()

	if *configPath == "" {
		log.Println("Usage: go run examples/run_proxy.go -config <path-to-yaml>")
		log.Println("Example: go run examples/run_proxy.go -config examples/basic.yaml")
		flag.Usage()
		os.Exit(1)
	}

	// Verify the path exists
	info, err := os.Stat(*configPath)
	if err != nil {
		log.Fatalf("Error accessing path '%s': %v", *configPath, err)
	}

	var config *proxy.Config

	// Load configuration
	if info.IsDir() {
		log.Printf("Loading configuration from directory: %s", *configPath)
		config, err = proxy.LoadConfigFromDir(*configPath)
	} else {
		log.Printf("Loading configuration from file: %s", *configPath)
		config, err = proxy.LoadConfigFromFile(*configPath)
	}

	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Create and start the server
	server := proxy.NewServer(*config)
	server.SetLogger(nil)

	log.Printf("Starting proxy server...")
	if err := server.Start(); err != nil {
		log.Fatalf("Server stopped with error: %v", err)
	}
}
