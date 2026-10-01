Real Dogecoin mainnet blocks, merged-mined with Litecoin parents, as hex:
blocks 371,337 (Dogecoin's first AuxPoW block), 748,634 and 894,863. They're
public chain data, taken from the test resources of
[libdohj](https://github.com/dogecoin/libdohj)
(`core/src/test/resources/org/bitcoinj/core/`, commit 3dd6d75), whose
`DogecoinBlockTest` supplies the expected hashes `auxpow_test.go` checks.
Each file is the full block: the 80-byte Dogecoin header, its CAuxPow, then
the transactions.
