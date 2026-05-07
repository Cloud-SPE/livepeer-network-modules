package main

import "github.com/Cloud-SPE/livepeer-network-rewrite/openai-runners/openai-runner/internal/runner"

func main() {
	runner.Run(runner.Config{
		Endpoint:     "/v1/embeddings",
		Capability:   "openai-text-embeddings",
		MaxBodyBytes: 1 << 20,
	})
}
