package main

import (
	_ "embed"

	"github.com/josuedeavila/simple-proxy/examples"
)

//go:embed basic.yaml
var configData []byte

// Run with the embedded yaml:
//
//	go run ./examples/basic
//
// Or with the JSON configuration read from the environment:
//
//	PROXY_CONFIG="$(cat examples/basic/basic.json)" go run ./examples/basic
func main() {
	examples.New(configData)
}
