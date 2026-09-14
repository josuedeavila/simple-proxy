package main

import (
	_ "embed"

	"github.com/josuedeavila/simple-proxy/examples"
)

//go:embed prefix.yaml
var configData []byte

// Run with the embedded yaml:
//
//	go run ./examples/prefix
//
// Or with the JSON configuration read from the environment:
//
//	PROXY_CONFIG="$(cat examples/prefix/prefix.json)" go run ./examples/prefix
func main() {
	examples.New(configData)
}
