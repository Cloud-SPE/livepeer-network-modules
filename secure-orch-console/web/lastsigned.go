package web

import (
	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/lastsigned"
)

func loadLastSigned(path string) ([]byte, error) {
	return lastsigned.Load(path)
}

func writeLastSignedAtomic(path string, data []byte) error {
	return lastsigned.WriteAtomic(path, data)
}
