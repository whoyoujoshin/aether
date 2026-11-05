//go:build windows

package gpu

import "syscall"

// openLibrary opens OpenCL.dll, which every GPU driver installs.
func openLibrary() (func(string) (uintptr, error), error) {
	dll, err := syscall.LoadDLL("OpenCL.dll")
	if err != nil {
		return nil, errNoOpenCL
	}
	return func(name string) (uintptr, error) {
		p, err := dll.FindProc(name)
		if err != nil {
			return 0, err
		}
		return p.Addr(), nil
	}, nil
}
