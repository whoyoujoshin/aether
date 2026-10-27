//go:build unix

package main

import "syscall"

// pause stops cmd/outbound's process, so nothing relays onto the
// counterparty until resume.
func pause(pid int) { must(syscall.Kill(pid, syscall.SIGSTOP), "pausing cmd/outbound") }

func resume(pid int) { must(syscall.Kill(pid, syscall.SIGCONT), "resuming cmd/outbound") }
