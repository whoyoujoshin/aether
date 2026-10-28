package accountauth

// PendingRotation and RecoveryThreshold are defined here so the keeper
// compiles before proto generation. Once proto is generated, delete this
// file and use the generated types. The fields match the proto.

// PendingRotation is a scheduled pubkey change.
type PendingRotation struct {
	NewPubkey   []byte
	ApplyHeight int64
	Nonce       uint64
}

func (p PendingRotation) Marshal() ([]byte, error) {
	// Minimal encoding: length-prefixed fields. Replaced by proto.
	return append(append(uint64Bytes(uint64(len(p.NewPubkey))), p.NewPubkey...),
		append(int64Bytes(p.ApplyHeight), uint64Bytes(p.Nonce)...)...), nil
}

func (p *PendingRotation) Unmarshal(bz []byte) error {
	if len(bz) < 8 {
		return errRotationNotImplemented
	}
	n := uint64From(bz[:8])
	bz = bz[8:]
	if uint64(len(bz)) < n+8+8 {
		return errRotationNotImplemented
	}
	p.NewPubkey = append([]byte{}, bz[:n]...)
	bz = bz[n:]
	p.ApplyHeight = int64From(bz[:8])
	p.Nonce = uint64From(bz[8:16])
	return nil
}

func uint64Bytes(n uint64) []byte {
	b := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		b[i] = byte(n)
		n >>= 8
	}
	return b
}

func uint64From(b []byte) uint64 {
	var n uint64
	for _, c := range b {
		n = n<<8 | uint64(c)
	}
	return n
}

func int64Bytes(n int64) []byte { return uint64Bytes(uint64(n)) }
func int64From(b []byte) int64  { return int64(uint64From(b)) }
