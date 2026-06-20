package app

import (
	"fmt"

	"github.com/cosmos/cosmos-sdk/types/module"
	storetypes "cosmossdk.io/store/types"

	"github.com/whoyoujoshin/aether/x/pow"
	"github.com/whoyoujoshin/aether/x/treasury"
	"github.com/whoyoujoshin/aether/x/governance"
)

const (
	Name            = "aether"
	DefaultNodeHome = ".aether"
)

var ModuleBasics = module.NewBasicManager(
	pow.AppModuleBasic{},
	treasury.AppModuleBasic{},
	governance.AppModuleBasic{},
)

type App struct {
	PowKeeper        pow.Keeper
	TreasuryKeeper   treasury.Keeper
	GovernanceKeeper governance.Keeper
}

func New() interface{} {
	fmt.Println("✅ Aether App initialized with Cosmos SDK")
	fmt.Println("PoW + Treasury + Governance modules loaded")

	app := &App{
		PowKeeper:        pow.NewKeeper(nil, nil), // storeKey later
		TreasuryKeeper:   treasury.NewKeeper(nil),
		GovernanceKeeper: governance.NewKeeper(nil),
	}

	return app
}