package pow_test

import (
	"testing"

	signing "cosmossdk.io/x/tx/signing"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/address"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/x/pow"
)

// TestMsgUpdateParams_SignerResolvesToAuthorityField proves the proto
// `option (cosmos.msg.v1.signer) = "authority"` annotation on
// MsgUpdateParams actually resolves correctly -- the same class of
// subtle wiring gap caught earlier in x/governance's own param-change
// work (a message that compiles and unit-tests fine in isolation but
// whose signer can't be resolved by the real governance execution
// path, which calls exactly this GetMsgV1Signers method, not the
// direct method call msg_server_test.go's other tests use).
func TestMsgUpdateParams_SignerResolvesToAuthorityField(t *testing.T) {
	prefix := sdk.GetConfig().GetBech32AccountAddrPrefix()
	registry, err := codectypes.NewInterfaceRegistryWithOptions(codectypes.InterfaceRegistryOptions{
		ProtoFiles: proto.HybridResolver,
		SigningOptions: signing.Options{
			AddressCodec:          address.NewBech32Codec(prefix),
			ValidatorAddressCodec: address.NewBech32Codec(prefix + "valoper"),
		},
	})
	require.NoError(t, err)
	pow.AppModuleBasic{}.RegisterInterfaces(registry)

	cdc := codec.NewProtoCodec(registry)

	authority := sdk.AccAddress("pow_signer_test_author")
	msg := &pow.MsgUpdateParams{
		Authority:            authority.String(),
		EpochLength:          1440,
		TopKSize:             21,
		BondCooldown:         4320,
		RecencyWindowK:       60,
		BeaconRoundsPerBlock: 5000,
	}

	signers, _, err := cdc.GetMsgV1Signers(msg)
	require.NoError(t, err)
	require.Len(t, signers, 1)
	require.Equal(t, authority.Bytes(), signers[0])
}
