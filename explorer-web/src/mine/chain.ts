// The pieces of the TypeScript client (clients/ts) the miner needs, from
// its source: ML-DSA-44 keys, transaction signing and native mining, all
// tested there against the Go implementation (clients/testdata).
export { Key, addressBytes, isAddress } from "../../../clients/ts/src/keys";
export { buildTx } from "../../../clients/ts/src/tx";
export {
  belowTarget, headerBytes, headerFor, msgSubmitPoW, nonceOffset, powHash, setNonce, type MiningHeader, type Work,
} from "../../../clients/ts/src/pow";
