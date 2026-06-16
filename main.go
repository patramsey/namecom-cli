package main

import "github.com/patramsey/namecom-cli/cmd"

// version is set at build time via:
//
//	go build -ldflags "-X main.version=x.y.z" .
var version = "dev"

func main() {
	cmd.Version = version
	cmd.Execute()
}
