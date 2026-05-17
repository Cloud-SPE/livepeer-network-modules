package main

import "github.com/Cloud-SPE/livepeer-network-modules/openai-runners/openai-runner/internal/runner"

func main() {
	runner.Run(runner.Config{
		Endpoint:     "/v1/chat/completions",
		Capability:   "openai-chat-completions",
		MaxBodyBytes: 5 << 20,
	})
}
