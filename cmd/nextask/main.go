package main

import "github.com/TolgaOk/nextask/internal/cli"

var version = "0.1.0"

func main() {
	cli.SetVersion(version)
	cli.Execute()
}
