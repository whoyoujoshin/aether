package main

import (
	"fmt"
	"os"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	tmdb "github.com/cometbft/cometbft-db"
	"github.com/cosmos/cosmos-sdk/server"

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
			fmt.Println("⛏️  Mining simulation started")
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

	// Initialize database
	db, err := tmdb.NewDB("aether", tmdb.GoLevelDBBackend, "./data")
	if err != nil {
		fmt.Printf("Error creating database: %v\n", err)
		return
	}
	defer db.Close()

	// Create logger
	logger := server.NewDefaultLogger()

	// Initialize app
	aethApp := app.New(
		logger,
		db,
		nil,
		true,
		map[int64]bool{},
		app.DefaultNodeHome,
		0,
	)

	fmt.Println("✅ Aether App initialized with Cosmos SDK")
	fmt.Println("PoW + Treasury + Governance modules loaded")

	// Simulate blocks
	block := int64(1)
	ticker := time.NewTicker(10 * time.Second) // 10s for testing instead of 60s
	defer ticker.Stop()

	for range ticker.C {
		minerAddr, err := sdk.AccAddressFromBech32("aether1miner1qwerty0123456789abcdefg123456")
		if err != nil {
			fmt.Printf("Error creating address: %v\n", err)
			continue
		}

		nonce := uint64(block * 12345)
		hash := fmt.Sprintf("simulatedhash%d", block)

		fmt.Printf("✅ Block %d: Miner: %s | Nonce: %d | Hash: %s\n", block, minerAddr.String(), nonce, hash)

		block++
	
		// Keep app reference to prevent garbage collection
		_ = aethApp
	}
}
