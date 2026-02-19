package main

import (
	"os"

	"ccgateway/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
