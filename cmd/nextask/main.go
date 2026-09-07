package main

import (
	"github.com/TolgaOk/nextask/internal/buildinfo"
	"github.com/TolgaOk/nextask/internal/cli"
)

func main() {
	cli.Execute(buildinfo.Version)
}
