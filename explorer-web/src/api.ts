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
}

export interface SearchResult {
  kind: "address" | "tx";
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

export const api = {
  stats: () => getJSON<Stats>("/api/stats"),
  validators: () => getJSON<ValidatorInfo[]>("/api/validators"),
  leaderboard: (epoch?: number) =>
    getJSON<Leaderboard>(`/api/leaderboard${epoch ? `?epoch=${epoch}` : ""}`),
  proposals: () => getJSON<Proposal[]>("/api/proposals"),
  proposalTally: (id: number) => getJSON<ProposalTally>(`/api/proposals/tally?id=${id}`),
  recentTransactions: (limit = 20) =>
    getJSON<RecentTransaction[]>(`/api/recent-transactions?limit=${limit}`),
  address: (addr: string) => getJSON<AddressPage>(`/api/address?addr=${encodeURIComponent(addr)}`),
  tx: (hash: string) => getJSON<TransactionDetail>(`/api/tx?hash=${encodeURIComponent(hash)}`),
  search: (q: string) => getJSON<SearchResult>(`/api/search?q=${encodeURIComponent(q)}`),
  blocks: () => getJSON<BlockSummary[]>("/api/blocks"),
  block: (height: number) => getJSON<BlockDetail>(`/api/block?height=${height}`),
  services: () => getJSON<ServiceDirectory>("/api/services"),
};
