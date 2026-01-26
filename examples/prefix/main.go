package main

import (
	_ "embed"

	"github.com/josuedeavila/simple-proxy/examples"
)

//go:embed prefix.yaml
var configData []byte

func main() {
	examples.New(configData)
}
