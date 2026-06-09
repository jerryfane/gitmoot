//go:build !windows

package skillopt

import (
	"fmt"
	"os"
	"syscall"
)

func openTrainInitScaffoldFileNoFollow(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_TRUNC|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("open returned nil file")
	}
	return file, nil
}
