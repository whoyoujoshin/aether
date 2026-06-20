package treasury

import "fmt"

type Keeper struct{}

func NewKeeper() Keeper {
	return Keeper{}
}

func (k Keeper) FundTreasury(amount int) {
	fmt.Printf("💰 Treasury funded with %d AETH\n", amount)
}