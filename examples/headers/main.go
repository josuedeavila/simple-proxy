package main

import (
	_ "embed"

	"github.com/josuedeavila/simple-proxy/examples"
)

//go:embed headers.yaml
var configData []byte

func main() {
	examples.New(configData)
}
