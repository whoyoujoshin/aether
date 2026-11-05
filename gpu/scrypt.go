package gpu

import (
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"unsafe"
)

//go:embed scrypt.cl
var kernelSource string

// HeaderSize is the mining header's length (package miner); the nonce is
// bytes 80..87, little-endian.
const HeaderSize = 128

// scratchPerHash is scrypt's scratchpad for one hash: N=1024 blocks of
// 128 bytes.
const scratchPerHash = 1024 * 128

// Hasher hashes batches of nonces on one device.
type Hasher struct {
	Device Device
	Batch  int // nonces per kernel launch

	ctx, queue, prog          uintptr
	kHashes, kSearch          uintptr
	scratch, hdr, tgt, result uintptr
}

// DefaultBatch is how many nonces a launch hashes on d when the caller
// doesn't say: as many scratchpads as fit in the largest buffer the device
// allows and in half its memory, a multiple of 64, at most 65,536.
func DefaultBatch(d Device) int {
	limit := d.MaxAlloc
	if half := d.Memory / 2; half < limit {
		limit = half
	}
	n := int(limit / scratchPerHash)
	if n > 1<<16 {
		n = 1 << 16
	}
	if n >= 64 {
		n -= n % 64
	}
	return n
}

// NewHasher compiles the kernel for d and allocates batch scratchpads
// (DefaultBatch when batch <= 0).
func NewHasher(d Device, batch int) (*Hasher, error) {
	if err := load(); err != nil {
		return nil, err
	}
	if batch <= 0 {
		batch = DefaultBatch(d)
	}
	if batch <= 0 {
		return nil, fmt.Errorf("%s has too little memory for one scrypt scratchpad", d.Name)
	}
	if uint64(batch)*scratchPerHash > d.MaxAlloc {
		return nil, fmt.Errorf("batch %d needs %d MiB in one buffer; %s allows %d MiB", batch, uint64(batch)*scratchPerHash>>20, d.Name, d.MaxAlloc>>20)
	}
	h := &Hasher{Device: d, Batch: batch}
	var errc int32
	dev := d.ID
	if h.ctx = cl.CreateContext(nil, 1, &dev, 0, 0, &errc); errc != clSuccess {
		return nil, clErr("clCreateContext", errc)
	}
	fail := func(err error) (*Hasher, error) { h.Close(); return nil, err }
	if h.queue = cl.CreateCommandQueue(h.ctx, dev, 0, &errc); errc != clSuccess {
		return fail(clErr("clCreateCommandQueue", errc))
	}
	src := cstr(kernelSource)
	if h.prog = cl.CreateProgramWithSrc(h.ctx, 1, &src, nil, &errc); errc != clSuccess {
		return fail(clErr("clCreateProgramWithSource", errc))
	}
	if code := cl.BuildProgram(h.prog, 1, &dev, cstr(""), 0, 0); code != clSuccess {
		log := infoString(func(size uintptr, v unsafe.Pointer, ret *uintptr) int32 {
			return cl.GetProgramBuildInfo(h.prog, dev, clProgramBuildLog, size, v, ret)
		})
		return fail(fmt.Errorf("building the kernel for %s: OpenCL error %d\n%s", d.Name, code, log))
	}
	if h.kHashes = cl.CreateKernel(h.prog, cstr("scrypt_hashes"), &errc); errc != clSuccess {
		return fail(clErr("clCreateKernel scrypt_hashes", errc))
	}
	if h.kSearch = cl.CreateKernel(h.prog, cstr("scrypt_search"), &errc); errc != clSuccess {
		return fail(clErr("clCreateKernel scrypt_search", errc))
	}
	buffer := func(flags uint64, size uint64) (uintptr, error) {
		b := cl.CreateBuffer(h.ctx, flags, uintptr(size), nil, &errc)
		return b, clErr("clCreateBuffer", errc)
	}
	var err error
	if h.scratch, err = buffer(clMemReadWrite, uint64(batch)*scratchPerHash); err != nil {
		return fail(err)
	}
	if h.hdr, err = buffer(clMemReadOnly, HeaderSize); err != nil {
		return fail(err)
	}
	if h.tgt, err = buffer(clMemReadOnly, 32); err != nil {
		return fail(err)
	}
	if h.result, err = buffer(clMemReadWrite, uint64(batch)*32); err != nil {
		return fail(err)
	}
	return h, nil
}

// Close releases the device's buffers and kernels.
func (h *Hasher) Close() {
	for _, m := range []*uintptr{&h.scratch, &h.hdr, &h.tgt, &h.result} {
		if *m != 0 {
			cl.ReleaseMemObject(*m)
			*m = 0
		}
	}
	for _, k := range []*uintptr{&h.kHashes, &h.kSearch} {
		if *k != 0 {
			cl.ReleaseKernel(*k)
			*k = 0
		}
	}
	if h.prog != 0 {
		cl.ReleaseProgram(h.prog)
		h.prog = 0
	}
	if h.queue != 0 {
		cl.ReleaseCommandQueue(h.queue)
		h.queue = 0
	}
	if h.ctx != 0 {
		cl.ReleaseContext(h.ctx)
		h.ctx = 0
	}
}

// words is b as big-endian 32-bit words, as the kernel reads headers and
// targets.
func words(b []byte) []uint32 {
	w := make([]uint32, len(b)/4)
	for i := range w {
		w[i] = binary.BigEndian.Uint32(b[4*i:])
	}
	return w
}

func (h *Hasher) write(buf uintptr, w []uint32) error {
	return clErr("clEnqueueWriteBuffer", cl.EnqueueWriteBuffer(h.queue, buf, 1, 0, uintptr(4*len(w)), unsafe.Pointer(&w[0]), 0, nil, nil))
}

func (h *Hasher) run(k uintptr, n int, args ...any) error {
	for i, a := range args {
		var code int32
		switch v := a.(type) {
		case uintptr:
			code = cl.SetKernelArg(k, uint32(i), unsafe.Sizeof(v), unsafe.Pointer(&v))
		case uint64:
			code = cl.SetKernelArg(k, uint32(i), 8, unsafe.Pointer(&v))
		default:
			return fmt.Errorf("kernel argument %d: unsupported type %T", i, a)
		}
		if code != clSuccess {
			return clErr(fmt.Sprintf("clSetKernelArg %d", i), code)
		}
	}
	global := uintptr(n)
	if err := clErr("clEnqueueNDRangeKernel", cl.EnqueueNDRangeKernel(h.queue, k, 1, nil, &global, nil, 0, nil, nil)); err != nil {
		return err
	}
	return clErr("clFinish", cl.Finish(h.queue))
}

func checkHeader(header []byte) error {
	if len(header) != HeaderSize {
		return fmt.Errorf("header is %d bytes, not %d", len(header), HeaderSize)
	}
	return nil
}

// Hashes returns the scrypt hashes of header with nonces base, base+1,
// ..., base+n-1 (n at most Batch). The nonce bytes in header are ignored.
func (h *Hasher) Hashes(header []byte, base uint64, n int) ([][32]byte, error) {
	if err := checkHeader(header); err != nil {
		return nil, err
	}
	if n <= 0 || n > h.Batch {
		return nil, fmt.Errorf("n %d must be 1..%d", n, h.Batch)
	}
	if err := h.write(h.hdr, words(header)); err != nil {
		return nil, err
	}
	if err := h.run(h.kHashes, n, h.hdr, base, h.scratch, h.result); err != nil {
		return nil, err
	}
	out := make([]uint32, n*8)
	if err := clErr("clEnqueueReadBuffer", cl.EnqueueReadBuffer(h.queue, h.result, 1, 0, uintptr(4*len(out)), unsafe.Pointer(&out[0]), 0, nil, nil)); err != nil {
		return nil, err
	}
	hashes := make([][32]byte, n)
	for i := range hashes {
		for j := 0; j < 8; j++ {
			binary.BigEndian.PutUint32(hashes[i][4*j:], out[i*8+j])
		}
	}
	return hashes, nil
}

// Search hashes one batch, nonces base .. base+Batch-1, and reports a
// nonce whose hash is below target (32 bytes, big-endian), if any.
func (h *Hasher) Search(header []byte, base uint64, target []byte) (uint64, bool, error) {
	if err := checkHeader(header); err != nil {
		return 0, false, err
	}
	if len(target) != 32 {
		return 0, false, errors.New("target must be 32 bytes")
	}
	if err := h.write(h.hdr, words(header)); err != nil {
		return 0, false, err
	}
	if err := h.write(h.tgt, words(target)); err != nil {
		return 0, false, err
	}
	zero := []uint32{0, 0, 0}
	if err := h.write(h.result, zero); err != nil {
		return 0, false, err
	}
	if err := h.run(h.kSearch, h.Batch, h.hdr, base, h.scratch, h.tgt, h.result); err != nil {
		return 0, false, err
	}
	found := make([]uint32, 3)
	if err := clErr("clEnqueueReadBuffer", cl.EnqueueReadBuffer(h.queue, h.result, 1, 0, 12, unsafe.Pointer(&found[0]), 0, nil, nil)); err != nil {
		return 0, false, err
	}
	if found[0] == 0 {
		return 0, false, nil
	}
	return uint64(found[1]) | uint64(found[2])<<32, true, nil
}
