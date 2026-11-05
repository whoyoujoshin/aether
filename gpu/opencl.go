// Package gpu runs Aether's native proof-of-work (scrypt over the 128-byte
// mining header, package miner) on OpenCL devices: graphics cards, and
// CPUs with an OpenCL driver such as pocl.
//
// It needs no cgo: the system's OpenCL library (OpenCL.dll on Windows,
// libOpenCL.so.1 on Linux, the OpenCL framework on macOS), which every GPU
// driver installs, is loaded at run time, so a CGO_ENABLED=0 build runs
// anywhere and simply finds no devices where there is no driver.
package gpu

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// OpenCL constants this package uses (cl.h).
const (
	clSuccess = 0

	clDeviceTypeCPU = 1 << 1
	clDeviceTypeGPU = 1 << 2
	clDeviceTypeAll = 0xFFFFFFFF

	clPlatformName          = 0x0902
	clDeviceType            = 0x1000
	clDeviceMaxComputeUnits = 0x1002
	clDeviceMaxMemAllocSize = 0x1010
	clDeviceGlobalMemSize   = 0x101F
	clDeviceName            = 0x102B
	clDriverVersion         = 0x102D
	clProgramBuildLog       = 0x1183

	clMemReadWrite    = 1 << 0
	clMemReadOnly     = 1 << 2
	clMemCopyHostPtr  = 1 << 5
	clDeviceNotFound  = -1
	clPlatformNotFind = -1001 // CL_PLATFORM_NOT_FOUND_KHR, from an ICD loader with no driver
)

// The OpenCL entry points, bound once by load.
var cl struct {
	GetPlatformIDs        func(n uint32, platforms *uintptr, count *uint32) int32
	GetPlatformInfo       func(p uintptr, param uint32, size uintptr, value unsafe.Pointer, ret *uintptr) int32
	GetDeviceIDs          func(p uintptr, typ uint64, n uint32, devices *uintptr, count *uint32) int32
	GetDeviceInfo         func(d uintptr, param uint32, size uintptr, value unsafe.Pointer, ret *uintptr) int32
	CreateContext         func(props *uintptr, n uint32, devices *uintptr, notify, user uintptr, errc *int32) uintptr
	CreateCommandQueue    func(ctx, device uintptr, props uint64, errc *int32) uintptr
	CreateProgramWithSrc  func(ctx uintptr, n uint32, src **byte, lengths *uintptr, errc *int32) uintptr
	BuildProgram          func(prog uintptr, n uint32, devices *uintptr, options *byte, notify, user uintptr) int32
	GetProgramBuildInfo   func(prog, device uintptr, param uint32, size uintptr, value unsafe.Pointer, ret *uintptr) int32
	CreateKernel          func(prog uintptr, name *byte, errc *int32) uintptr
	CreateBuffer          func(ctx uintptr, flags uint64, size uintptr, host unsafe.Pointer, errc *int32) uintptr
	SetKernelArg          func(k uintptr, index uint32, size uintptr, value unsafe.Pointer) int32
	EnqueueWriteBuffer    func(q, buf uintptr, blocking uint32, offset, size uintptr, ptr unsafe.Pointer, nwait uint32, wait, ev *uintptr) int32
	EnqueueReadBuffer     func(q, buf uintptr, blocking uint32, offset, size uintptr, ptr unsafe.Pointer, nwait uint32, wait, ev *uintptr) int32
	EnqueueNDRangeKernel  func(q, k uintptr, dims uint32, offset, global, local *uintptr, nwait uint32, wait, ev *uintptr) int32
	Finish                func(q uintptr) int32
	ReleaseMemObject      func(m uintptr) int32
	ReleaseKernel         func(k uintptr) int32
	ReleaseProgram        func(p uintptr) int32
	ReleaseCommandQueue   func(q uintptr) int32
	ReleaseContext        func(c uintptr) int32
}

var (
	loadOnce sync.Once
	loadErr  error
)

// load binds the OpenCL library's functions, once.
func load() error {
	loadOnce.Do(func() {
		var sym func(name string) (uintptr, error)
		sym, loadErr = openLibrary()
		if loadErr != nil {
			return
		}
		bind := func(fptr any, name string) {
			if loadErr != nil {
				return
			}
			addr, err := sym(name)
			if err != nil {
				loadErr = fmt.Errorf("OpenCL library has no %s: %w", name, err)
				return
			}
			purego.RegisterFunc(fptr, addr)
		}
		bind(&cl.GetPlatformIDs, "clGetPlatformIDs")
		bind(&cl.GetPlatformInfo, "clGetPlatformInfo")
		bind(&cl.GetDeviceIDs, "clGetDeviceIDs")
		bind(&cl.GetDeviceInfo, "clGetDeviceInfo")
		bind(&cl.CreateContext, "clCreateContext")
		bind(&cl.CreateCommandQueue, "clCreateCommandQueue")
		bind(&cl.CreateProgramWithSrc, "clCreateProgramWithSource")
		bind(&cl.BuildProgram, "clBuildProgram")
		bind(&cl.GetProgramBuildInfo, "clGetProgramBuildInfo")
		bind(&cl.CreateKernel, "clCreateKernel")
		bind(&cl.CreateBuffer, "clCreateBuffer")
		bind(&cl.SetKernelArg, "clSetKernelArg")
		bind(&cl.EnqueueWriteBuffer, "clEnqueueWriteBuffer")
		bind(&cl.EnqueueReadBuffer, "clEnqueueReadBuffer")
		bind(&cl.EnqueueNDRangeKernel, "clEnqueueNDRangeKernel")
		bind(&cl.Finish, "clFinish")
		bind(&cl.ReleaseMemObject, "clReleaseMemObject")
		bind(&cl.ReleaseKernel, "clReleaseKernel")
		bind(&cl.ReleaseProgram, "clReleaseProgram")
		bind(&cl.ReleaseCommandQueue, "clReleaseCommandQueue")
		bind(&cl.ReleaseContext, "clReleaseContext")
	})
	return loadErr
}

// clErr turns an OpenCL status into an error naming the call.
func clErr(call string, code int32) error {
	if code == clSuccess {
		return nil
	}
	return fmt.Errorf("%s: OpenCL error %d", call, code)
}

// Device is one OpenCL device.
type Device struct {
	ID       uintptr
	Platform string
	Name     string
	Driver   string
	GPU      bool
	Units    uint32 // compute units
	Memory   uint64 // global memory, bytes
	MaxAlloc uint64 // the largest single buffer, bytes
}

func (d Device) String() string {
	kind := "CPU"
	if d.GPU {
		kind = "GPU"
	}
	return fmt.Sprintf("%s %s (%s; %d compute units, %d MiB, driver %s)", kind, d.Name, d.Platform, d.Units, d.Memory>>20, d.Driver)
}

// Devices lists every OpenCL device on every platform. An error means no
// OpenCL library could be loaded; a machine with the library but no
// driver gives an empty list.
func Devices() ([]Device, error) {
	if err := load(); err != nil {
		return nil, err
	}
	var n uint32
	if code := cl.GetPlatformIDs(0, nil, &n); code == clPlatformNotFind || n == 0 {
		return nil, nil
	} else if code != clSuccess {
		return nil, clErr("clGetPlatformIDs", code)
	}
	platforms := make([]uintptr, n)
	if err := clErr("clGetPlatformIDs", cl.GetPlatformIDs(n, &platforms[0], nil)); err != nil {
		return nil, err
	}
	var out []Device
	for _, p := range platforms {
		pname := infoString(func(size uintptr, v unsafe.Pointer, ret *uintptr) int32 {
			return cl.GetPlatformInfo(p, clPlatformName, size, v, ret)
		})
		var nd uint32
		if code := cl.GetDeviceIDs(p, clDeviceTypeAll, 0, nil, &nd); code == clDeviceNotFound || nd == 0 {
			continue
		} else if code != clSuccess {
			return nil, clErr("clGetDeviceIDs", code)
		}
		ids := make([]uintptr, nd)
		if err := clErr("clGetDeviceIDs", cl.GetDeviceIDs(p, clDeviceTypeAll, nd, &ids[0], nil)); err != nil {
			return nil, err
		}
		for _, id := range ids {
			d := Device{ID: id, Platform: pname}
			str := func(param uint32) string {
				return infoString(func(size uintptr, v unsafe.Pointer, ret *uintptr) int32 {
					return cl.GetDeviceInfo(id, param, size, v, ret)
				})
			}
			d.Name, d.Driver = str(clDeviceName), str(clDriverVersion)
			var typ, mem, alloc uint64
			cl.GetDeviceInfo(id, clDeviceType, 8, unsafe.Pointer(&typ), nil)
			cl.GetDeviceInfo(id, clDeviceMaxComputeUnits, 4, unsafe.Pointer(&d.Units), nil)
			cl.GetDeviceInfo(id, clDeviceGlobalMemSize, 8, unsafe.Pointer(&mem), nil)
			cl.GetDeviceInfo(id, clDeviceMaxMemAllocSize, 8, unsafe.Pointer(&alloc), nil)
			d.GPU, d.Memory, d.MaxAlloc = typ&clDeviceTypeGPU != 0, mem, alloc
			out = append(out, d)
		}
	}
	return out, nil
}

// infoString reads a string-valued OpenCL info parameter.
func infoString(get func(size uintptr, v unsafe.Pointer, ret *uintptr) int32) string {
	var size uintptr
	if get(0, nil, &size) != clSuccess || size == 0 {
		return ""
	}
	buf := make([]byte, size)
	if get(size, unsafe.Pointer(&buf[0]), nil) != clSuccess {
		return ""
	}
	return strings.TrimRight(string(buf), "\x00 ")
}

// cstr is s as a NUL-terminated C string.
func cstr(s string) *byte {
	b := append([]byte(s), 0)
	return &b[0]
}

var errNoOpenCL = errors.New("no OpenCL library found: install your graphics driver (it brings OpenCL), or pocl for CPUs")
