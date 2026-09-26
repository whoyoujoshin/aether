package main

import (
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestQueryCommandsRejectBadAddressesBeforeQuerying(t *testing.T) {
	const good = "aether17dgnrqt0dchve4tpgtmff5quwjyksz7n5u9xmgqav7g99ns42xpq7j5sqv"
	for _, tc := range []struct {
		cmd  *cobra.Command
		args []string
		want string
	}{
		{authzQueryCmd(), []string{"grants", good, "cosmos1xyz"}, `invalid grantee address "cosmos1xyz"`},
		{authzQueryCmd(), []string{"grants-by-granter", "nope"}, `invalid granter address "nope"`},
		{feegrantQueryCmd(), []string{"grant", "nope", good}, `invalid granter address "nope"`},
		{feegrantQueryCmd(), []string{"grants-by-grantee", "nope"}, `invalid grantee address "nope"`},
		{bankQueryCmd(), []string{"balances", "nope"}, `invalid account address "nope"`},
		{bankQueryCmd(), []string{"balance", good}, "give a denom"},
		{authzQueryCmd(), []string{"grants", good}, "accepts between 2 and 3 arg(s)"},
	} {
		tc.cmd.SetArgs(tc.args)
		tc.cmd.SetOut(io.Discard)
		tc.cmd.SetErr(io.Discard)
		err := tc.cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s %v: got %v, want an error containing %q", tc.cmd.Name(), tc.args, err, tc.want)
		}
	}
}
