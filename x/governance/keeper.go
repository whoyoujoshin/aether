package governance

import "fmt"

type Keeper struct{}

func NewKeeper() Keeper {
	return Keeper{}
}

func (k Keeper) SubmitProposal(title string) {
	fmt.Printf("📜 Proposal submitted: %s\n", title)
}