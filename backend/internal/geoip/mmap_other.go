//go:build !unix

package geoip

import (
	"fmt"
	"os"
)

// mapFile reads the file into memory where mmap is not available. The service
// ships as a Linux binary; this keeps the package building everywhere else.
func mapFile(path string, limit int64) ([]byte, func() error, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	if size := fi.Size(); size <= 0 || size > limit {
		return nil, nil, fmt.Errorf("%s: size %d outside 1..%d", path, size, limit)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	return data, func() error { return nil }, nil
}
