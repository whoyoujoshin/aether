package accountauth

import (
	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// UnpackInterfaces implements codectypes.UnpackInterfacesMessage, the
// same way x/authz's own MsgExec does for its Msgs field: without this,
// each Any's GetCachedValue() is nil after the tx decoder runs, since
// gogoproto doesn't unpack a bare `repeated google.protobuf.Any` field
// on its own.
func (msg MsgExecAuthenticated) UnpackInterfaces(unpacker cdctypes.AnyUnpacker) error {
	for _, any := range msg.Msgs {
		var m sdk.Msg
		if err := unpacker.UnpackAny(any, &m); err != nil {
			return err
		}
	}
	return nil
}
