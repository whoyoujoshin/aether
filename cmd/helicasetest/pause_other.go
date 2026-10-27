//go:build !unix

package main

import "log"

func pause(int)  { log.Fatal("--outbound-pid needs a unix system") }
func resume(int) {}
