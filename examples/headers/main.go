package main

import (
	_ "embed"

	"github.com/josuedeavila/simple-proxy/examples"
)

//go:embed headers.yaml
var configData []byte

// Run with the embedded yaml:
//
//	go run ./examples/headers
//
// Or with the JSON configuration read from the environment:
//
//	PROXY_CONFIG="$(cat examples/headers/headers.json)" go run ./examples/headers
func main() {
	examples.New(configData)
}
