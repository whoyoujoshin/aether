package main

import (
	"fmt"
	"os"
	"time"
)

func main() {
	fmt.Println("🚀 Aether Daemon v0.1")
	fmt.Println("60s blocks • Fair Launch • Scrypt + AuxPoW")

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "init":
			fmt.Println("✅ Node initialized for testnet")
			fmt.Println("Genesis ready with 5 AETH initial reward")
		case "start":
			fmt.Println("🌐 Starting local testnet node...")
			fmt.Println("Chain ID: aether-test-1")
			fmt.Println("Block time: 60 seconds")
			fmt.Println("Press Ctrl+C to stop\n")

			block := 1
			ticker := time.NewTicker(60 * time.Second)
			for range ticker.C {
				fmt.Printf("⛏️  Block %d mined | Reward: 5 AETH | Time: %s\n", block, time.Now().Format("15:04:05"))
				block++
			}
		default:
			fmt.Println("Usage: aetherd [init | start]")
		}
	} else {
		fmt.Println("\nCommands:")
		fmt.Println("  aetherd init   - Initialize node")
		fmt.Println("  aetherd start  - Start the node")
	}
}