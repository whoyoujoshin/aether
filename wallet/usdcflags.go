package wallet

import "flag"

// USDCFlags adds the flags that pick a tool's USDC to fs, with defaults
// from def (for tools that also read the environment): --usdc-channel,
// or --usdc-path with --usdc-base-denom, and --usdc-issuer. Read the
// setting after fs is parsed.
func USDCFlags(fs *flag.FlagSet, def USDCSetting) *USDCSetting {
	s := &USDCSetting{}
	fs.StringVar(&s.Channel, "usdc-channel", def.Channel,
		"Aether's end of its direct channel to Noble (e.g. channel-3): USDC is Noble's uusdc over exactly that channel. For any other route, use --usdc-path. Empty (and no --usdc-path): AETH only")
	fs.StringVar(&s.Path, "usdc-path", def.Path,
		"the USDC to accept as its ICS-20 denom trace on Aether, Aether's hop first: e.g. transfer/channel-1/transfer/channel-4280 for Noble's USDC through Osmosis. USDC is --usdc-base-denom over exactly that path")
	fs.StringVar(&s.BaseDenom, "usdc-base-denom", def.BaseDenom,
		"USDC's denom on the chain that issues it, with --usdc-path (default uusdc, Noble's): e.g. erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d for Circle's USDC on Injective. Case-sensitive")
	fs.StringVar(&s.Issuer, "usdc-issuer", def.Issuer,
		`one-word name of the chain that issues that USDC, shown as "USDC (<issuer>)": e.g. Injective (default Noble for uusdc, else the base denom)`)
	return s
}
