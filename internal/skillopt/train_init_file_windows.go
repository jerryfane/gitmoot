//go:build windows

package skillopt

import "os"

func openTrainInitScaffoldFileNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
}
