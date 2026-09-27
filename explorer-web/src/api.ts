// Typed fetch wrappers for cmd/explorer's JSON API. Field names here
// match the Go backend's json tags exactly (camelCase) -- see
// cmd/explorer/main.go for the source of truth.

async function getJSON<T>(path: string): Promise<T> {
  const res = await fetch(path);
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error || `request failed: ${res.status}`);
  }
  return res.json();
}

export interface Stats {
  latestHeight: number;
  difficulty: string;
  blockReward: string;
  currentEpoch: number;
  treasuryBalance: string;
  epochLength: number;
  targetBlockTime: number; // seconds between PoW submissions the difficulty aims for
  activeValidators: number;
}

export interface ValidatorInfo {
  address: string;
  tenureRatio: string;
  enteredAtUnix: number;
}

export interface LeaderboardEntry {
  address: string;
  work: number;
}

export interface Leaderboard {
  epoch: number;
  entries: LeaderboardEntry[];
}

export interface Proposal {
  id: number;
  recipient: string;
  amount: string;
  totalDeposit: string;
  status: string;
  proposalType: string;
  submitTime: number;
  depositEndTime: number;
  votingStartTime: number;
  votingEndTime: number;
}

export interface Vote {
  proposalId: number;
  voter: string;
  option: string;
  weight: string;
}

export interface Tally {
  validVoterCount: number;
  yesPower: string;
  noPower: string;
  abstainPower: string;
  vetoPower: string;
  quorumThreshold: number;
}

export interface ProposalTally {
  tally: Tally;
  votes: Vote[];
}

export interface RecentTransaction {
  hash: string;
  height: number;
  code: number;
  msgType: string;
  timestamp: string;
}

export interface Transaction {
  hash: string;
  height: number;
  code: number;
  direction: string;
  amount: string;
  timestamp: string;
}

export interface AddressPage {
  address: string;
  balance: string;
  transactions: Transaction[];
  escrow: { balance: string; unlockHeight: number; pending: boolean };
  banned: boolean;
  isValidator: boolean;
  txsSigned: number;
}

export interface Transfer {
  from: string;
  to: string;
  amount: string;
}

export interface AuxPowInfo {
  parentHeaderBase64: string;
  coinbaseTxBase64: string;
  auxBlockHashBase64: string;
}

export interface TransactionDetail {
  hash: string;
  height: number;
  code: number;
  rawLog: string;
  gasUsed: number;
  gasWanted: number;
  from: string;
  to: string;
  amount: string;
  timestamp: string;
  transfers: Transfer[];
  auxPow: AuxPowInfo | null;
  codespace: string;
  msgTypes: string[];
  messages: TxMessage[];
  events: TxEvent[];
  fee: string; // uaeth
  gasLimit: number;
  memo: string;
  signer: string;
  sequence: number | null;
  latestHeight: number;
  raw: unknown;
}

/** A message as the chain's proto JSON: "@type" plus its own fields. */
export type TxMessage = { "@type": string } & Record<string, unknown>;

export interface TxEvent {
  type: string;
  attributes: { key: string; value: string }[];
}

export interface SearchResult {
  kind: "address" | "tx" | "block";
  value: string;
}

export interface BlockSummary {
  height: number;
  hash: string;
  time: string;
  numTxs: number;
  proposerAddress: string;
}

export interface BlockDetail {
  height: number;
  hash: string;
  time: string;
  proposerAddress: string;
  appHash: string;
  lastCommitHash: string;
  dataHash: string;
  numTxs: number;
  txHashes: string[];
  parentHash: string;
  sizeBytes: number;
  gasUsed: number;
  gasWanted: number;
  maxGas: number; // -1: no block gas limit
  latestHeight: number;
  epochLength: number;
  proposer: string; // the proposer's miner account
  txs: BlockTx[];
  pow: PowProof[];
}

export interface BlockTx {
  hash: string;
  msgType: string;
  msgCount: number;
  code: number;
  from: string;
  to: string;
  amount: string;
}

/** One MsgSubmitPoW in a block; hash/target only for native submissions. */
export interface PowProof {
  txHash: string;
  code: number;
  miner: string;
  kind: "native" | "auxpow";
  claimedHeight?: number;
  nonce?: number;
  difficulty?: number;
  hash?: string;
  target?: string;
  valid: boolean;
  margin?: string;
  reward: string;
}

export interface ValidatorSetEntry {
  consensusAddress: string;
  account: string;
  bootstrap: boolean;
  votingPower: number;
  tenureRatio: string;
  enteredAtUnix: number;
  signed: boolean[]; // oldest first
  missed: number;
  banned: boolean;
  status: "active" | "missing" | "pending" | "banned";
}

export interface ValidatorSet {
  height: number;
  window: number;
  totalPower: number;
  topKSize: number;
  validators: ValidatorSetEntry[];
  signingHeights: number[];
}

export interface GovernanceParams {
  minDeposit: number; // uaeth
  depositPeriod: number; // seconds
  votingPeriod: number; // seconds
  activeValidators: number;
}

export interface ServiceListing {
  name: string;
  description: string;
  url: string;
  price: string;
  priceAeth: string;
  schemes: string[];
  minDeposit?: string;
  payTo: string;
  listedAtHeight: number;
  txHash: string;
  activity?: {
    windowBlocks: number;
    payments: number;
    payers: number;
    volumeAeth: string;
    ratings: number;
    averageScore?: number;
  };
}

export interface ServiceDirectory {
  directoryAddress: string;
  services: ServiceListing[];
}

export interface SendPermission {
  unlimited: boolean;
  spendLimit: string; // uaeth left; "" if unlimited
  allowList: string[];
  expiration: string; // RFC 3339; "" if none
}

export interface FeePermission {
  kind: string;
  spendLimit: string;
  expiration: string;
}

export interface Permission {
  account: string;
  send: SendPermission | null;
  fees: FeePermission | null;
  other: string[];
}

export interface Grants {
  address: string;
  active: boolean;
  given: Permission[];
  received: Permission[];
}

export interface AgentCard {
  name: string;
  chainId: string;
  height: number; // 0: the node didn't answer
  addressPrefix: string;
  denom: string;
  displayDenom: string;
  decimals: number;
  signatures: string;
  endpoints: { rpc: string; grpc: string; faucet: string; explorer: string; seed: string };
  faucet: { url: string; request: string; reachable: boolean | null } | null;
  authz: { activationHeight: number; active: boolean };
  mcp: { install: string; init: string; tools: string[] };
  paymentSchemes: string[];
  docs: Record<string, string>;
  warnings: string[];
}

export const api = {
  stats: () => getJSON<Stats>("/api/stats"),
  validators: () => getJSON<ValidatorInfo[]>("/api/validators"),
  validatorSet: () => getJSON<ValidatorSet>("/api/validator-set"),
  governanceParams: () => getJSON<GovernanceParams>("/api/governance/params"),
  leaderboard: (epoch?: number) =>
    getJSON<Leaderboard>(`/api/leaderboard${epoch ? `?epoch=${epoch}` : ""}`),
  proposals: () => getJSON<Proposal[]>("/api/proposals"),
  proposalTally: (id: number) => getJSON<ProposalTally>(`/api/proposals/tally?id=${id}`),
  recentTransactions: (limit = 20) =>
    getJSON<RecentTransaction[]>(`/api/recent-transactions?limit=${limit}`),
  address: (addr: string) => getJSON<AddressPage>(`/api/address?addr=${encodeURIComponent(addr)}`),
  grants: (addr: string) => getJSON<Grants>(`/api/grants?addr=${encodeURIComponent(addr)}`),
  tx: (hash: string) => getJSON<TransactionDetail>(`/api/tx?hash=${encodeURIComponent(hash)}`),
  search: (q: string) => getJSON<SearchResult>(`/api/search?q=${encodeURIComponent(q)}`),
  blocks: () => getJSON<BlockSummary[]>("/api/blocks"),
  block: (height: number) => getJSON<BlockDetail>(`/api/block?height=${height}`),
  services: () => getJSON<ServiceDirectory>("/api/services"),
  agents: () => getJSON<AgentCard>("/api/agents"),
};
