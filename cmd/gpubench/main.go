// cmd/gpubench measures how fast this machine mines Aether's native
// proof-of-work on each OpenCL device (graphics cards, and CPUs with an
// OpenCL driver) and on its CPU cores with the Go miner, so the cost of
// native hash can be measured rather than guessed.
//
// It checks each device's hashes against Go's scrypt before timing it,
// and talks to no node: nothing is mined or sent.
//
//	go run ./cmd/gpubench              // every device, 20 s each, plus the CPU
//	go run ./cmd/gpubench -list        // just list the devices
//	go run ./cmd/gpubench -seconds 60 -device 0 -batch 16384
//
// No cgo: it loads the OpenCL library the graphics driver installed, so a
// CGO_ENABLED=0 build (GOOS=windows too) runs as is.
package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"flag"
	"fmt"
	"math"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/whoyoujoshin/aether/gpu"
	"github.com/whoyoujoshin/aether/miner"
)

func main() {
	list := flag.Bool("list", false, "list the OpenCL devices and exit")
	device := flag.Int("device", -1, "only this device (its number in -list); default: every device")
	seconds := flag.Float64("seconds", 20, "how long to time each device")
	batch := flag.Int("batch", 0, "nonces per kernel launch (default: as many scratchpads as fit; 128 KiB each)")
	cpuThreads := flag.Int("cpu-threads", runtime.NumCPU(), "Go CPU miner threads to time for comparison; 0 skips it")
	flag.Parse()

	devs, err := gpu.Devices()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	for i, d := range devs {
		fmt.Printf("[%d] %s\n", i, d)
	}
	if len(devs) == 0 {
		fmt.Println("no OpenCL devices found")
	}
	if *list {
		return
	}

	header := make([]byte, gpu.HeaderSize)
	_, _ = rand.Read(header)
	dur := time.Duration(*seconds * float64(time.Second))

	type result struct {
		name string
		rate float64
	}
	var results []result
	for i, d := range devs {
		if *device >= 0 && i != *device {
			continue
		}
		rate, err := timeDevice(d, *batch, header, dur)
		if err != nil {
			fmt.Printf("[%d] %s: %v\n", i, d.Name, err)
			continue
		}
		results = append(results, result{fmt.Sprintf("[%d] %s", i, d.Name), rate})
	}
	var cpuRate float64
	if *cpuThreads > 0 {
		cpuRate = timeCPU(header, *cpuThreads, dur)
		results = append(results, result{fmt.Sprintf("Go CPU miner, %d threads", *cpuThreads), cpuRate})
	}

	fmt.Println("\nResults (hashes a second):")
	for _, r := range results {
		line := fmt.Sprintf("  %-50s %12s", r.name, rateString(r.rate))
		if cpuRate > 0 && r.rate != cpuRate {
			line += fmt.Sprintf("   %.1fx this machine's CPU miner", r.rate/cpuRate)
		}
		fmt.Println(line)
	}
	fmt.Println("\nPaste these lines back. Nothing was mined or sent.")
}

// timeDevice checks d's hashes against Go's scrypt, then times it.
func timeDevice(d gpu.Device, batch int, header []byte, dur time.Duration) (float64, error) {
	h, err := gpu.NewHasher(d, batch)
	if err != nil {
		return 0, err
	}
	defer h.Close()
	fmt.Printf("\n%s: batch %d (%d MiB of scratchpads)\n", d.Name, h.Batch, uint64(h.Batch)*128>>10)

	const checks = 16
	got, err := h.Hashes(header, 1<<40, checks)
	if err != nil {
		return 0, err
	}
	b := make([]byte, len(header))
	copy(b, header)
	for i := range got {
		binary.LittleEndian.PutUint64(b[80:], 1<<40+uint64(i))
		want := miner.Hash(b)
		if string(want) != string(got[i][:]) {
			return 0, fmt.Errorf("hash %d differs from Go's scrypt: the kernel is wrong on this device, so no speed is reported", i)
		}
	}
	fmt.Printf("  %d hashes match Go's scrypt\n", checks)

	impossible := make([]byte, 32)
	base := uint64(1 << 41)
	if _, _, err := h.Search(header, base, impossible); err != nil { // warm-up launch, not timed
		return 0, err
	}
	start := time.Now()
	var hashes uint64
	for time.Since(start) < dur {
		base += uint64(h.Batch)
		if _, _, err := h.Search(header, base, impossible); err != nil {
			return 0, err
		}
		hashes += uint64(h.Batch)
	}
	rate := float64(hashes) / time.Since(start).Seconds()
	fmt.Printf("  %s over %s\n", rateString(rate), time.Since(start).Round(time.Second))
	return rate, nil
}

// timeCPU times package miner's Go scrypt on n threads.
func timeCPU(header []byte, n int, dur time.Duration) float64 {
	fmt.Printf("\nGo CPU miner, %d threads\n", n)
	ctx, cancel := context.WithTimeout(context.Background(), dur)
	defer cancel()
	h := miner.Header{
		PrevHash: header[16:48], MerkleRoot: header[48:80],
		Difficulty: math.MaxUint64, Miner: header[96:128],
	}
	var count atomic.Uint64
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			miner.Search(ctx, h, uint64(i), uint64(n), math.MaxUint64, &count)
		}(i)
	}
	wg.Wait()
	rate := float64(count.Load()) / time.Since(start).Seconds()
	fmt.Printf("  %s\n", rateString(rate))
	return rate
}

func rateString(r float64) string {
	switch {
	case r >= 1e6:
		return fmt.Sprintf("%.2f MH/s", r/1e6)
	case r >= 1e3:
		return fmt.Sprintf("%.1f kH/s", r/1e3)
	default:
		return fmt.Sprintf("%.0f H/s", r)
	}
}
