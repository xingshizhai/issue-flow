//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package fake

import (
	"errors"
	"os"
	"syscall"
)

func lockFile(file *os.File) (func() error, error) {
	mode := syscall.LOCK_EX
	var err error
	for {
		err = syscall.Flock(int(file.Fd()), mode)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	return func() error {
		return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	}, nil
}

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}
