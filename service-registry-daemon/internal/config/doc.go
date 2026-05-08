// Package config holds validated configuration structs and the YAML
// loader for the operator-curated static overlay. Anything in this
// package is pure: parse + validate + return a frozen struct. No I/O
// libraries (no network, no DB), only stdlib + gopkg.in/yaml.v3.
//
// The runtime layer is responsible for reading the file off disk; this
// package takes []byte and produces structs.
package config
