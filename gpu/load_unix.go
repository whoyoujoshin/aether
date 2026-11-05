//go:build linux || darwin || freebsd || netbsd

package gpu

import (
	"runtime"

	"github.com/ebitengine/purego"
)

// openLibrary opens the system's OpenCL library.
func openLibrary() (func(string) (uintptr, error), error) {
	names := []string{"libOpenCL.so.1", "libOpenCL.so"}
	if runtime.GOOS == "darwin" {
		names = []string{"/System/Library/Frameworks/OpenCL.framework/OpenCL"}
	}
	for _, name := range names {
		h, err := purego.Dlopen(name, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err == nil {
			return func(sym string) (uintptr, error) { return purego.Dlsym(h, sym) }, nil
		}
	}
	return nil, errNoOpenCL
}
