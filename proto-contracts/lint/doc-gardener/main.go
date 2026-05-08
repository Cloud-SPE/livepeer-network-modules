package main

import (
	"flag"
	"os"
)

func main() {
	root := flag.String("root", ".", "repository root containing docs/")
	flag.Parse()
	os.Exit(Run(*root, os.Stderr))
}
