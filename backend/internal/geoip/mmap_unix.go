//go:build unix

package geoip

import (
	"fmt"
	"os"
	"syscall"
)

// mapFile maps path read-only, the way the mmdb readers do, so a database costs
// page cache rather than heap. Nothing read from the mapping may be used after
// release; see locDB.
//
// Replacing the file on disk does not disturb a live mapping: the old inode
// stays alive until it is unmapped, which is what lets a refresh install a new
// file while lookups are still reading the previous one.
func mapFile(path string, limit int64) ([]byte, func() error, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	size := fi.Size()
	if size <= 0 || size > limit {
		return nil, nil, fmt.Errorf("%s: size %d outside 1..%d", path, size, limit)
	}

	data, err := syscall.Mmap(int(f.Fd()), 0, int(size), syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return nil, nil, fmt.Errorf("mmap %s: %w", path, err)
	}
	return data, func() error { return syscall.Munmap(data) }, nil
}
