package main

import (
	"context"
	"fmt"
	"os"

	"wanderer-import/internal/cli"
)

func main() {
	app := cli.New(os.Stdout, os.Stderr)
	if err := app.Run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "wanderer-import:", err)
		os.Exit(1)
	}
}
