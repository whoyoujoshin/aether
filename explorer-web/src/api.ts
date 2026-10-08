import type { AssetInfo } from "./format";
// Typed fetch wrappers for cmd/explorer's JSON API. Field names here
// match the Go backend's json tags exactly (camelCase) -- see
// cmd/explorer/main.go for the source of truth.

async function getJSON<T>(path: string): Promise<T> {
  const res = await fetch(path);
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error || body.message || `request failed: ${res.status}`);
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
  balance: string; // uaeth
  /** Every token held, AETH first; symbol/decimals/origin only for a known asset. Absent from older explorers. */
  balances?: { denom: string; amount: string; symbol?: string; decimals?: number; origin?: string }[];
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
  price: string; // base units of asset
  priceAeth?: string; // AETH prices only
  asset?: string; // the denom the price is in; absent means uaeth
  symbol?: string; // as the service states it: untrusted, so shown only for a denom the explorer knows
  priceAmount?: string;
  schemes: string[];
  minDeposit?: string;
  payTo: string;
  listedAtHeight: number;
  txHash: string;
  activity?: {
    windowBlocks: number;
    payments: number;
    payers: number;
    volume?: string; // in the service's asset: a decimal if the explorer knows it, else base units
    volumeAeth?: string;
    ratings: number;
    averageScore?: number;
  };
}

export interface ServiceDirectory {
  directoryAddress: string;
  services: ServiceListing[];
}

export interface IBCCoin {
  denom: string;
  amount: string;
}

export interface IBCChannel {
  portId: string;
  channelId: string;
  state: string; // e.g. "STATE_OPEN"
  ordering: string;
  version: string;
  connectionId: string;
  clientId: string;
  counterpartyPortId: string;
  counterpartyChannelId: string;
  counterpartyChainId: string; // "" if the client state couldn't be read
  trustingPeriodSecs: number;
  unbondingPeriodSecs: number;
  packetsSent: number;
  pendingPackets: number;
  escrowAddress: string;
  escrowBalances: IBCCoin[];
}

export interface IBCSummary {
  clients: number;
  connections: number;
  channels: IBCChannel[];
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

export interface HelixBlock {
  height: number;
  hash: string;
  time: string;
  numTxs: number;
  proposer: string; // Aether: the proposer's miner account; the IBC chain: its consensus address (hex)
  packets: number; // IBC packet events in the block
}

export interface HelixStrand {
  chainId: string;
  name: string;
  height: number;
  blockTimeSecs: number; // 0 with fewer than two blocks
  validators: number;
  blocks: HelixBlock[]; // newest first
}

export interface PacketStep {
  chain: "aether" | "ibc";
  height: number;
  time: string;
  txHash?: string; // Aether steps only
}

export interface HelixPacket {
  direction: "out" | "in"; // out: Aether to the IBC chain
  sequence: number;
  srcPort: string;
  srcChannel: string;
  dstPort: string;
  dstChannel: string;
  denom?: string;
  amount?: string;
  sender?: string;
  receiver?: string;
  sent?: PacketStep;
  received?: PacketStep;
  acked?: PacketStep;
  timedOut?: PacketStep;
  status: "in-flight" | "received" | "acked" | "timed-out";
}

export interface HelixBridge {
  portId: string;
  channelId: string;
  counterpartyChannelId: string;
  packetsSent: number;
  inFlight: number;
  escrowedUaeth: string;
  avgRelaySecs: number; // 0 if no packet in the window has both ends
}

export interface HelixPeer {
  chainId: string; // empty while that chain can't be reached
  name: string;
}

export interface Helix {
  windowSecs: number;
  now: string;
  peers?: HelixPeer[]; // every chain the explorer can draw opposite Aether; older APIs omit it
  aether: HelixStrand;
  ibc: HelixStrand | null; // null when the explorer has no --ibc-rpc
  bridge: HelixBridge | null;
  packets: HelixPacket[]; // newest first
  errors?: Record<string, string>;
}

// --- the faucet, through the explorer's /api/faucet/ (cmd/faucet) ---

export type DripSource = "web" | "agent" | "api";

export interface SourceCounts {
  agent: number;
  web: number;
  api: number;
}

export interface FaucetStats {
  drips: number;
  sent_uaeth: number;
  unique_wallets: number;
  new_wallets: number;
  new_wallets_by_agents: number;
  since?: string;
  sources_30d: SourceCounts;
  new_wallets_daily: (SourceCounts & { date: string })[];
  top_agents_30d: { agent_id: string; name: string; new_wallets: number; drips: number }[] | null;
  amount_uaeth: number;
  address_cooldown_secs: number;
  ibc_enabled: boolean;
  pow_bits: number;
  balance_uaeth?: number;
}

export interface Drip {
  time: string;
  tx_hash: string;
  address: string;
  amount_uaeth: number;
  strand: string;
  source: DripSource;
  agent_id?: string;
  agent_name?: string;
  new_wallet: boolean;
}

export interface FaucetChallenge {
  challenge: string;
  bits: number;
  expires_at: string;
}

export interface FaucetReply {
  success: boolean;
  code: string;
  message: string;
  tx_hash?: string;
  retry_after_seconds?: number;
  new_wallet?: boolean;
}

async function faucetRequest(body: unknown): Promise<FaucetReply> {
  const res = await fetch("/api/faucet/request", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  const reply = (await res.json().catch(() => null)) as FaucetReply | null;
  if (!reply) throw new Error(`faucet answered ${res.status}`);
  return reply;
}

// --- where nodes run, from the explorer's --node-locations file ---

export interface NodeLocation {
  address: string; // miner account
  label?: string;
  city?: string;
  country: string;
  region: string;
  lat: number;
  lon: number;
  precision: "city" | "country";
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
  ibc: () => getJSON<IBCSummary>("/api/ibc"),
  agents: () => getJSON<AgentCard>("/api/agents"),
  assets: () => getJSON<{ assets: AssetInfo[] }>("/api/assets"),
  helix: (seconds = 96, min = 0, peer?: string | null) =>
    getJSON<Helix>(`/api/helix?seconds=${seconds}&min=${min}${peer ? `&peer=${encodeURIComponent(peer)}` : ""}`),
  locations: () => getJSON<{ locations: NodeLocation[] }>("/api/locations"),
  faucetStats: () => getJSON<FaucetStats>("/api/faucet/stats"),
  faucetDrips: (limit = 20) => getJSON<{ drips: Drip[] | null }>(`/api/faucet/drips?limit=${limit}`),
  faucetChallenge: (address: string) =>
    getJSON<FaucetChallenge>(`/api/faucet/challenge?address=${encodeURIComponent(address)}`),
  faucetRequest,
};
