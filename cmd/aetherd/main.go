package main

import (
	"fmt"
	"os"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/app"
)

func main() {
	fmt.Println("🚀 Aether Daemon v0.1 - Local Testnet (Cosmos SDK skeleton)")
	fmt.Println("60s blocks • Scrypt PoW + AuxPoW • Fair Launch")

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "init":
			fmt.Println("✅ Node initialized")
		case "start":
			startTestnet()
		case "mine":
			fmt.Println("⛏️ Mining simulation started")
			for {
				time.Sleep(60 * time.Second)
				fmt.Println("✅ Simulated mining block")
			}
		default:
			fmt.Println("Usage: aetherd [init | start | mine]")
		}
	} else {
		fmt.Println("\nCommands:")
		fmt.Println("  aetherd init   - Initialize node")
		fmt.Println("  aetherd start  - Start the node")
		fmt.Println("  aetherd mine   - Mining simulation")
	}
}

func startTestnet() {
	fmt.Println("🌐 Starting local testnet...")
	fmt.Println("Aether App initialized with Cosmos SDK")
	fmt.Println("PoW verification enabled")
	fmt.Println("Press Ctrl+C to stop\n")

	block := 1
	ticker := time.NewTicker(60 * time.Second)
	for range ticker.C {
		miner := "aether1miner"
		nonce := uint64(block * 12345)
		hash := "simulatedhash" + fmt.Sprint(block)

		// Call PoW verification
		a := app.New().(*app.App)
		a.PowKeeper.ProcessBlock(nil, int64(block), sdk.AccAddress(miner), nonce, hash)

		block++
	}
}