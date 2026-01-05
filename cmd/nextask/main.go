package main

import "github.com/TolgaOk/nextask/internal/cli"

var version = "0.1.0-alpha.1"

func main() {
	cli.SetVersion(version)
	cli.Execute()
}
