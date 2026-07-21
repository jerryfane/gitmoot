//go:build !unix && !windows

package presence

func ProbeDaemonProcess(_ int, _ string) (string, error) {
	return DaemonUnknown, nil
}
