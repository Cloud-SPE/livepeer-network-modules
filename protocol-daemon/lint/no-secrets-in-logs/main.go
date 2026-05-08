package main

import (
	"flag"
	"os"
)

func main() {
	root := flag.String("root", ".", "directory to walk")
	flag.Parse()
	os.Exit(Run(*root, os.Stderr))
}
