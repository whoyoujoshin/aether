// Package ligase lets a token transfer arriving over IBC carry an
// instruction for Aether in its memo, so someone on another chain can fund
// and settle an escrow here without ever holding an Aether key or any AETH.
//
// A transfer sent to the Ligase address (Address) with a memo of the form
// {"aether": {"<action>": {...}}} does one of:
//
//   - escrow: lock the transferred tokens in an x/escrow escrow for an
//     Aether payee, with the sender as payer;
//   - release: pay escrow id to its payee, as its payer;
//   - withdraw: send everything waiting in the sender's mailbox back over
//     the channel it came in on.
//
// Each remote sender has a mailbox: a keyless Aether account derived from
// the channel the packet arrived on and the sender's address on the other
// chain (Mailbox). Its tokens land there, it's the payer of the escrows
// it funds, and refunds come back to it until it withdraws. Only a packet
// from that sender over that channel can act for the mailbox, and the
// packet's sender is proven by the other chain's validators, the same
// proof that moves its tokens.
//
// The two-strands rule: an action authorized this way -- by a key another
// chain accepts, not an ML-DSA-44 signature on Aether -- only ever moves
// tokens that arrived over IBC (ibc/... denoms). It can't escrow, release
// or withdraw AETH, so AETH stays under post-quantum signatures only (see
// Classical).
//
// The instruction and the transfer are one: if the action fails, the
// acknowledgement is an error, IBC discards everything the packet did,
// and the other chain refunds the sender.
package ligase

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/address"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	channeltypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	porttypes "github.com/cosmos/ibc-go/v8/modules/core/05-port/types"
	ibcexported "github.com/cosmos/ibc-go/v8/modules/core/exported"

	"github.com/whoyoujoshin/aether/x/escrow"
)

// ModuleName names the Ligase address and the mailbox derivation.
const ModuleName = "ligase"

// MemoKey is the memo's top-level key for Aether instructions. Other keys
// (another chain's packet-forwarding instructions, say) are left alone.
const MemoKey = "aether"

// EventType is emitted for every action, with AttributeAction and the
// others below.
const (
	EventType           = "ligase"
	AttributeAction     = "action"
	AttributeChannel    = "channel"
	AttributeSender     = "remote_sender"
	AttributeMailbox    = "mailbox"
	AttributeEscrowID   = "escrow_id"
	AttributeReturned   = "returned"
	AttributeError      = "error"
	withdrawPacketLife  = time.Hour
	maxMemoInstructions = 1
)

// Address is where a transfer carrying an instruction must be sent.
var Address = authtypes.NewModuleAddress(ModuleName)

var (
	ErrInstruction = errorsmod.Register(ModuleName, 2, "invalid ligase instruction")
	ErrClassical   = errorsmod.Register(ModuleName, 3, "an instruction from another chain can only move tokens that arrived over IBC")
	ErrAction      = errorsmod.Register(ModuleName, 4, "ligase action failed")
)

// Mailbox is the Aether account that acts for sender (an address on the
// other chain) on the packets it sends over channel (Aether's end).
func Mailbox(channel, sender string) sdk.AccAddress {
	return address.Module(ModuleName, []byte(channel+"/"+sender))
}

// Classical reports whether coins may be moved on the authority of
// another chain's key: only if every one of them is an IBC voucher. AETH
// (uaeth), and anything else native to Aether, needs an ML-DSA-44
// signature.
func Classical(coins sdk.Coins) bool {
	for _, c := range coins {
		if !strings.HasPrefix(c.Denom, "ibc/") {
			return false
		}
	}
	return true
}

// Host is what Ligase needs from the app.
type Host interface {
	// Active reports whether Ligase acts at this block. Before it does,
	// transfers pass through untouched.
	Active(ctx sdk.Context) bool
	Escrow() escrow.MsgServer
	GetEscrow(ctx sdk.Context, id uint64) (escrow.Escrow, bool)
	Balances(ctx sdk.Context, addr sdk.AccAddress) sdk.Coins
	DenomTrace(ctx sdk.Context, ibcDenom string) (transfertypes.DenomTrace, bool)
	Transfer(ctx sdk.Context, msg *transfertypes.MsgTransfer) error
}

// Instruction is the memo's "aether" object: exactly one action.
type Instruction struct {
	Escrow   *EscrowInstruction  `json:"escrow,omitempty"`
	Release  *ReleaseInstruction `json:"release,omitempty"`
	Withdraw *struct{}           `json:"withdraw,omitempty"`
}

// EscrowInstruction creates an escrow of the transferred tokens.
type EscrowInstruction struct {
	Payee string `json:"payee"`
	// ExpiresIn is a Go duration ("72h"); ExpiresAt a unix time. One of
	// them.
	ExpiresIn string `json:"expires_in,omitempty"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
	// OnExpiry is "refund" (the default) or "release".
	OnExpiry string `json:"on_expiry,omitempty"`
	Arbiter  string `json:"arbiter,omitempty"`
	Terms    string `json:"terms,omitempty"`
}

// ReleaseInstruction releases an escrow the mailbox is payer or arbiter
// of.
type ReleaseInstruction struct {
	ID string `json:"id"`
}

// Result is the success acknowledgement's content.
type Result struct {
	Action   string `json:"action"`
	Mailbox  string `json:"mailbox"`
	EscrowID string `json:"escrow_id,omitempty"`
	Returned string `json:"returned,omitempty"`
}

// IBCModule wraps the transfer module's IBC callbacks.
type IBCModule struct {
	porttypes.IBCModule
	host Host
}

// NewIBCModule wraps transfer (the ICS-20 module's IBC callbacks).
func NewIBCModule(transfer porttypes.IBCModule, host Host) IBCModule {
	return IBCModule{IBCModule: transfer, host: host}
}

// ParseMemo returns the memo's Aether instruction, or nil if it has none.
func ParseMemo(memo string) (*Instruction, error) {
	if !strings.Contains(memo, `"`+MemoKey+`"`) {
		return nil, nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(memo), &top); err != nil {
		return nil, nil // not JSON: an ordinary memo that mentions the word
	}
	raw, ok := top[MemoKey]
	if !ok {
		return nil, nil
	}
	var in Instruction
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return nil, errorsmod.Wrapf(ErrInstruction, "memo %q: %s", MemoKey, err)
	}
	n := 0
	for _, set := range []bool{in.Escrow != nil, in.Release != nil, in.Withdraw != nil} {
		if set {
			n++
		}
	}
	if n != maxMemoInstructions {
		return nil, errorsmod.Wrap(ErrInstruction, "exactly one of escrow, release or withdraw")
	}
	return &in, nil
}

// OnRecvPacket acts on a transfer to Address with an instruction, and
// passes every other packet to the transfer module unchanged.
func (m IBCModule) OnRecvPacket(ctx sdk.Context, packet channeltypes.Packet, relayer sdk.AccAddress) ibcexported.Acknowledgement {
	if !m.host.Active(ctx) {
		return m.IBCModule.OnRecvPacket(ctx, packet, relayer)
	}
	var data transfertypes.FungibleTokenPacketData
	if err := transfertypes.ModuleCdc.UnmarshalJSON(packet.GetData(), &data); err != nil {
		return m.IBCModule.OnRecvPacket(ctx, packet, relayer) // transfer answers it
	}
	in, err := ParseMemo(data.Memo)
	toLigase := data.Receiver == Address.String()
	switch {
	case err != nil:
		return m.fail(ctx, packet, data, err)
	case in == nil && !toLigase:
		return m.IBCModule.OnRecvPacket(ctx, packet, relayer)
	case in == nil:
		return m.fail(ctx, packet, data, errorsmod.Wrapf(ErrInstruction, "a transfer to %s needs an %q instruction in its memo", Address, MemoKey))
	case !toLigase:
		return m.fail(ctx, packet, data, errorsmod.Wrapf(ErrInstruction, "a transfer with an %q instruction must be sent to %s", MemoKey, Address))
	}

	// The tokens go to the sender's mailbox.
	mailbox := Mailbox(packet.DestinationChannel, data.Sender)
	data.Receiver = mailbox.String()
	redirected := packet
	redirected.Data = data.GetBytes()
	ack := m.IBCModule.OnRecvPacket(ctx, redirected, relayer)
	if ack == nil || !ack.Success() {
		return ack
	}

	coin, err := receivedCoin(packet, data)
	if err != nil {
		return m.fail(ctx, packet, data, err)
	}
	res := Result{Mailbox: mailbox.String()}
	switch {
	case in.Escrow != nil:
		res.Action = "escrow"
		res.EscrowID, err = m.createEscrow(ctx, mailbox, coin, in.Escrow)
	case in.Release != nil:
		res.Action = "release"
		res.EscrowID, err = m.release(ctx, mailbox, in.Release)
	case in.Withdraw != nil:
		res.Action = "withdraw"
		res.Returned, err = m.withdraw(ctx, packet, data.Sender, mailbox)
	}
	if err != nil {
		return m.fail(ctx, packet, data, err)
	}

	ctx.EventManager().EmitEvent(sdk.NewEvent(EventType,
		sdk.NewAttribute(AttributeAction, res.Action),
		sdk.NewAttribute(AttributeChannel, packet.DestinationChannel),
		sdk.NewAttribute(AttributeSender, data.Sender),
		sdk.NewAttribute(AttributeMailbox, res.Mailbox),
		sdk.NewAttribute(AttributeEscrowID, res.EscrowID),
		sdk.NewAttribute(AttributeReturned, res.Returned),
	))
	bz, err := json.Marshal(map[string]Result{ModuleName: res})
	if err != nil {
		return m.fail(ctx, packet, data, err)
	}
	return channeltypes.NewResultAcknowledgement(bz)
}

// fail answers with an error acknowledgement: IBC discards what the packet
// did here, and the other chain refunds the sender. The reason goes in an
// event, since acknowledgements carry only the error's code.
func (m IBCModule) fail(ctx sdk.Context, packet channeltypes.Packet, data transfertypes.FungibleTokenPacketData, err error) ibcexported.Acknowledgement {
	ctx.EventManager().EmitEvent(sdk.NewEvent(EventType,
		sdk.NewAttribute(AttributeChannel, packet.DestinationChannel),
		sdk.NewAttribute(AttributeSender, data.Sender),
		sdk.NewAttribute(AttributeError, err.Error()),
	))
	return channeltypes.NewErrorAcknowledgement(err)
}

// receivedCoin is the coin the transfer module just credited: the sender
// chain's tokens as a voucher (ibc/...), or, for tokens that left Aether
// and are coming back, the original denom.
func receivedCoin(packet channeltypes.Packet, data transfertypes.FungibleTokenPacketData) (sdk.Coin, error) {
	amount, ok := sdkmath.NewIntFromString(data.Amount)
	if !ok {
		return sdk.Coin{}, errorsmod.Wrapf(ErrInstruction, "amount %q", data.Amount)
	}
	var denom string
	if transfertypes.ReceiverChainIsSource(packet.SourcePort, packet.SourceChannel, data.Denom) {
		unprefixed := data.Denom[len(transfertypes.GetDenomPrefix(packet.SourcePort, packet.SourceChannel)):]
		denom = transfertypes.ParseDenomTrace(unprefixed).IBCDenom()
	} else {
		prefixed := transfertypes.GetPrefixedDenom(packet.DestinationPort, packet.DestinationChannel, data.Denom)
		denom = transfertypes.ParseDenomTrace(prefixed).IBCDenom()
	}
	return sdk.NewCoin(denom, amount), nil
}

func (m IBCModule) createEscrow(ctx sdk.Context, mailbox sdk.AccAddress, coin sdk.Coin, in *EscrowInstruction) (string, error) {
	amount := sdk.NewCoins(coin)
	if !Classical(amount) {
		return "", errorsmod.Wrapf(ErrClassical, "%s is native to Aether", coin.Denom)
	}
	expiresAt := in.ExpiresAt
	switch {
	case in.ExpiresIn != "" && expiresAt != 0:
		return "", errorsmod.Wrap(ErrInstruction, "expires_in or expires_at, not both")
	case in.ExpiresIn != "":
		d, err := time.ParseDuration(in.ExpiresIn)
		if err != nil {
			return "", errorsmod.Wrapf(ErrInstruction, "expires_in: %s", err)
		}
		expiresAt = ctx.BlockTime().Add(d).Unix()
	case expiresAt == 0:
		return "", errorsmod.Wrap(ErrInstruction, "expires_in or expires_at is required")
	}
	onExpiry := escrow.ON_EXPIRY_REFUND
	switch strings.ToLower(in.OnExpiry) {
	case "", "refund":
	case "release":
		onExpiry = escrow.ON_EXPIRY_RELEASE
	default:
		return "", errorsmod.Wrapf(ErrInstruction, "on_expiry %q: refund or release", in.OnExpiry)
	}
	res, err := m.host.Escrow().CreateEscrow(ctx, &escrow.MsgCreateEscrow{
		Payer: mailbox.String(), Payee: in.Payee, Arbiter: in.Arbiter,
		Amount: amount, ExpiresAt: expiresAt, OnExpiry: onExpiry, Terms: in.Terms,
	})
	if err != nil {
		return "", errorsmod.Wrap(ErrAction, err.Error())
	}
	return strconv.FormatUint(res.Id, 10), nil
}

func (m IBCModule) release(ctx sdk.Context, mailbox sdk.AccAddress, in *ReleaseInstruction) (string, error) {
	id, err := strconv.ParseUint(in.ID, 10, 64)
	if err != nil {
		return "", errorsmod.Wrapf(ErrInstruction, "id %q", in.ID)
	}
	e, ok := m.host.GetEscrow(ctx, id)
	if !ok {
		return "", errorsmod.Wrapf(ErrAction, "escrow %d not found", id)
	}
	// The mailbox may have been named arbiter of someone's AETH escrow; it
	// still can't move AETH.
	if !Classical(e.Amount) {
		return "", errorsmod.Wrapf(ErrClassical, "escrow %d holds %s", id, e.Amount)
	}
	if _, err := m.host.Escrow().ReleaseEscrow(ctx, &escrow.MsgReleaseEscrow{Sender: mailbox.String(), Id: id}); err != nil {
		return "", errorsmod.Wrap(ErrAction, err.Error())
	}
	return in.ID, nil
}

// withdraw sends the mailbox's vouchers that came in over this channel
// back over it to sender. Anything else in the mailbox -- AETH someone
// sent it, tokens from another channel -- stays.
func (m IBCModule) withdraw(ctx sdk.Context, packet channeltypes.Packet, sender string, mailbox sdk.AccAddress) (string, error) {
	hop := transfertypes.GetDenomPrefix(packet.DestinationPort, packet.DestinationChannel)
	var sent sdk.Coins
	for _, c := range m.host.Balances(ctx, mailbox) {
		if !Classical(sdk.NewCoins(c)) {
			continue
		}
		trace, ok := m.host.DenomTrace(ctx, c.Denom)
		if !ok || !strings.HasPrefix(trace.GetFullDenomPath(), hop) {
			continue
		}
		err := m.host.Transfer(ctx, &transfertypes.MsgTransfer{
			SourcePort: packet.DestinationPort, SourceChannel: packet.DestinationChannel,
			Token: c, Sender: mailbox.String(), Receiver: sender,
			TimeoutHeight:    clienttypes.ZeroHeight(),
			TimeoutTimestamp: uint64(ctx.BlockTime().Add(withdrawPacketLife).UnixNano()),
			Memo:             fmt.Sprintf(`{"%s":{"withdrawn_from":"%s"}}`, ModuleName, mailbox),
		})
		if err != nil {
			return "", errorsmod.Wrapf(ErrAction, "returning %s: %s", c, err)
		}
		sent = sent.Add(c)
	}
	return sent.String(), nil
}
