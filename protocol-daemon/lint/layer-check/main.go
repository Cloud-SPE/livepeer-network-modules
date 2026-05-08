package main

import (
	"flag"
	"os"
)

func main() {
	root := flag.String("root", ".", "module root")
	flag.Parse()
	os.Exit(Run(*root, os.Stderr))
}
