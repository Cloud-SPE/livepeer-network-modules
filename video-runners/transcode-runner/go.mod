module github.com/Cloud-SPE/livepeer-network-rewrite/video-runners/transcode-runner

go 1.22

require github.com/Cloud-SPE/livepeer-network-rewrite/video-runners/transcode-core v0.0.0

require gopkg.in/yaml.v3 v3.0.1 // indirect

replace github.com/Cloud-SPE/livepeer-network-rewrite/video-runners/transcode-core => ../transcode-core
