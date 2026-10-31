import { useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api";
import { AddressLink, TxHash } from "../components/Hash";
import { aeth, duration, int } from "../format";
import { solveChallenge } from "../faucetwork";
import { fetchAccount, Miner, type MineAccount, type MinerState, type Submission } from "../mine/engine";
import { forgetKey, loadKey, newKey, saveKey } from "../mine/keystore";
import type { Key } from "../mine/chain";

// Mining in the browser. The hashing runs on this computer; the explorer
// only hands out the block to mine on (one cached read for everyone) and
// relays finds. Testnet AETH has no market value.

const RELEASES = "https://github.com/whoyoujoshin/aether/releases";

const SUB_COLS = "110px 120px minmax(0,1fr) minmax(0,1.6fr)";

const cores = (): number => Math.max(1, Math.min(navigator.hardwareConcurrency || 2, 16));

function rateText(hs: number): string {
  if (hs >= 1e6) return `${(hs / 1e6).toFixed(2)} MH/s`;
  if (hs >= 1e3) return `${(hs / 1e3).toFixed(2)} kH/s`;
  return `${Math.round(hs)} H/s`;
}

function statusTag(s: Submission) {
  switch (s.status) {
    case "accepted":
      return <span className="tag mine-ok">ACCEPTED</span>;
    case "refused":
      return <span className="tag mine-no">REFUSED</span>;
    case "pending":
      return <span className="tag">IN MEMPOOL</span>;
    default:
      return <span className="tag">SENDING</span>;
  }
}

/** Create or recover the miner account. */
function KeySetup({ onReady }: { onReady: (key: Key) => void }) {
  const [fresh, setFresh] = useState<{ key: Key; mnemonic: string } | null>(null);
  const [saved, setSaved] = useState(false);
  const [recover, setRecover] = useState(false);
  const [phrase, setPhrase] = useState("");
  const [error, setError] = useState<string | null>(null);

  function download(mnemonic: string, address: string) {
    const blob = new Blob(
      [`Aether miner account\n${address}\n\nRecovery phrase (anyone with it can spend this account's AETH):\n${mnemonic}\n`],
      { type: "text/plain" },
    );
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "aether-miner-recovery.txt";
    a.click();
    URL.revokeObjectURL(url);
  }

  if (fresh) {
    return (
      <div className="card-glow card-pad mine-setup">
        <div className="eyebrow red">Step 1 of 2 · Your miner account</div>
        <h2 className="mine-h2">Save your recovery phrase</h2>
        <p className="muted">
          These 24 words are your miner account: rewards are paid to it, and they're the only way to get it back in another
          browser. This browser keeps a copy, so anyone who can use it can spend what the account holds.
        </p>
        <div className="mine-phrase mono">{fresh.mnemonic}</div>
        <div className="mono faint" style={{ margin: "8px 0 16px", wordBreak: "break-all" }}>{fresh.key.address}</div>
        <div className="mine-row">
          <button type="button" className="btn-outline" onClick={() => download(fresh.mnemonic, fresh.key.address)}>
            Download as a file
          </button>
          <label className="mine-check">
            <input type="checkbox" checked={saved} onChange={(e) => setSaved(e.target.checked)} /> I've saved it
          </label>
          <button
            type="button"
            className="btn-red"
            disabled={!saved}
            onClick={() => onReady(saveKey(fresh.mnemonic).key)}
          >
            Continue
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className="card-glow card-pad mine-setup">
      <div className="eyebrow red">Step 1 of 2 · Your miner account</div>
      <h2 className="mine-h2">Mine Aether in this browser</h2>
      <p className="muted">
        Your computer does the hashing. Each find is signed here by your miner account and sent to the Aether testnet; if
        the chain accepts it, the block reward goes to that account. The chain takes one find a block, so a find can still lose
        to another miner's.
      </p>
      {!recover ? (
        <div className="mine-row">
          <button type="button" className="btn-red" onClick={() => setFresh(newKey())}>
            Create a miner account
          </button>
          <button type="button" className="btn-outline" onClick={() => setRecover(true)}>
            I have a recovery phrase
          </button>
        </div>
      ) : (
        <>
          <textarea
            className="mine-input mono"
            rows={3}
            placeholder="24 (or 12) recovery words"
            value={phrase}
            spellCheck={false}
            onChange={(e) => {
              setPhrase(e.target.value);
              setError(null);
            }}
          />
          <div className="mine-row">
            <button
              type="button"
              className="btn-red"
              onClick={() => {
                try {
                  onReady(saveKey(phrase).key);
                } catch (e) {
                  setError(e instanceof Error ? e.message : String(e));
                }
              }}
            >
              Use this account
            </button>
            <button type="button" className="btn-outline" onClick={() => setRecover(false)}>
              Back
            </button>
          </div>
          {error && <div className="error-banner" style={{ marginTop: 12 }}>{error}</div>}
        </>
      )}
      <p className="faint" style={{ marginTop: 18, fontSize: 13 }}>
        Faster on your own hardware: the <a href={RELEASES}>Aether Pay desktop app</a> has a Mine tab that hashes natively, and
        every release has the <code>powminer</code> command-line miner.
      </p>
    </div>
  );
}

/** A new account exists on chain only once it receives funds: one faucet drip. */
function Activate({ address, onDone }: { address: string; onDone: () => void }) {
  const [phase, setPhase] = useState<"idle" | "pow" | "sending" | "waiting" | "error">("idle");
  const [hashes, setHashes] = useState(0);
  const [error, setError] = useState("");
  const abort = useRef<AbortController | null>(null);
  useEffect(() => () => abort.current?.abort(), []);

  async function go() {
    const ctl = new AbortController();
    abort.current = ctl;
    try {
      setPhase("pow");
      const ch = await api.faucetChallenge(address);
      const nonce = await solveChallenge(ch.challenge, ch.bits, setHashes, ctl.signal);
      setPhase("sending");
      const reply = await api.faucetRequest({ address, strand: "aether", pow: { challenge: ch.challenge, nonce } });
      if (!(reply.success || reply.code === "pending")) {
        const wait = reply.retry_after_seconds ? ` Try again in ${duration(reply.retry_after_seconds)}.` : "";
        throw new Error(`${reply.message}.${wait}`);
      }
      setPhase("waiting");
      for (let i = 0; i < 40 && !ctl.signal.aborted; i++) {
        await new Promise((r) => setTimeout(r, 3000));
        const acct = await fetchAccount(address).catch(() => null);
        if (acct?.exists) {
          onDone();
          return;
        }
      }
      throw new Error("the drip hasn't landed yet; reload in a minute");
    } catch (e) {
      if (!ctl.signal.aborted) {
        setError(e instanceof Error ? e.message : String(e));
        setPhase("error");
      }
    }
  }

  const label = { idle: "Activate with a faucet drip", pow: `Proving you're a browser… ${int(hashes)} hashes`, sending: "Sending…", waiting: "Waiting for the next block…", error: "Try again" }[phase];
  return (
    <div className="card-glow card-pad mine-setup">
      <div className="eyebrow red">Step 2 of 2 · Activate the account</div>
      <h2 className="mine-h2">One faucet drip opens the account</h2>
      <p className="muted">
        A new account appears on chain when it first receives funds, and it must exist to sign a submission. The faucet sends
        it free testnet AETH once; mining pays from then on.
      </p>
      <button type="button" className="btn-red" disabled={phase !== "idle" && phase !== "error"} onClick={go}>
        {label}
      </button>
      {phase === "error" && <div className="error-banner" style={{ marginTop: 12 }}>{error}</div>}
    </div>
  );
}

function Kpi({ label, value, sub }: { label: string; value: string; sub: string }) {
  return (
    <div className="card kpi">
      <div className="kpi-label">{label}</div>
      <div className="kpi-value mono">{value}</div>
      <div className="kpi-sub">{sub}</div>
    </div>
  );
}

function MinerPanel({ keyObj, account, onForget, onRefresh }: { keyObj: Key; account: MineAccount; onForget: () => void; onRefresh: () => void }) {
  const [state, setState] = useState<MinerState | null>(null);
  const [threads, setThreads] = useState(() => Math.max(1, Math.floor(cores() / 2)));
  const [reward, setReward] = useState<string | null>(null);
  const miner = useMemo(() => new Miner(keyObj, setState), [keyObj]);
  useEffect(() => () => miner.stop(), [miner]);
  useEffect(() => {
    api.stats().then((s) => setReward(s.blockReward)).catch(() => {});
  }, []);
  const accepted = state?.submissions.filter((s) => s.status === "accepted").length ?? 0;
  useEffect(() => {
    if (accepted) onRefresh();
  }, [accepted, onRefresh]);

  const running = !!state?.running;
  const difficulty = state?.work ? Number(state.work.difficulty) : 0;
  const perFind = state?.rate && difficulty ? difficulty / state.rate : 0;

  return (
    <div className="mine-stack">
      <div className="card-glow card-pad">
        <div className="mine-head">
          <div>
            <div className="eyebrow red">Miner account</div>
            <AddressLink address={keyObj.address} />
            <div className="muted" style={{ marginTop: 4 }}>
              Balance <b className="mono">{aeth(account.balance)}</b>
              {reward && <span className="faint"> · about {aeth(reward)} a find, before the treasury's 15%</span>}
            </div>
          </div>
          <button type="button" className="linkish" disabled={running} onClick={onForget} title="Removes the key from this browser">
            Forget this account
          </button>
        </div>

        <div className="mine-controls">
          <label className="mine-threads">
            <span>Threads</span>
            <input type="range" min={1} max={cores()} value={threads} disabled={running} onChange={(e) => setThreads(Number(e.target.value))} />
            <span className="mono">
              {threads} of {cores()}
            </span>
          </label>
          {!running ? (
            <button type="button" className="btn-red mine-go" onClick={() => void miner.start(threads)}>
              Start mining
            </button>
          ) : (
            <button type="button" className="btn-outline mine-go" onClick={() => miner.stop()}>
              Stop
            </button>
          )}
        </div>
        <p className="faint" style={{ fontSize: 13, margin: "8px 0 0" }}>
          Keep this tab open. Mining uses the threads you pick at full speed, and your fans may notice.
        </p>
        {state?.error && <div className="error-banner" style={{ marginTop: 12 }}>{state.error}</div>}
      </div>

      <div className="kpi-grid">
        <Kpi label="Hash rate" value={running ? rateText(state?.rate ?? 0) : "—"} sub={`${int(state?.hashes ?? 0)} hashes this session`} />
        <Kpi label="Difficulty" value={difficulty ? int(difficulty) : "—"} sub="expected hashes per find" />
        <Kpi label="Time per find" value={perFind ? `~${duration(Math.round(perFind))}` : "—"} sub="on average, at this rate" />
        <Kpi label="Finds" value={int(state?.finds ?? 0)} sub={`${accepted} accepted by the chain`} />
      </div>

      <div className="card card-pad">
        <div className="card-head">
          <span className="card-title">Submissions</span>
          <span className="card-meta">{state?.work ? `mining on block ${int(state.work.height)}` : "not mining"}</span>
        </div>
        {!state?.submissions.length ? (
          <div className="empty">No finds yet. At this difficulty a find takes {perFind ? `about ${duration(Math.round(perFind))}` : "a while"}.</div>
        ) : (
          <div className="scroll-x">
            <div className="rows">
              {state.submissions.map((s, i) => (
                <div key={i} className="row" style={{ gridTemplateColumns: SUB_COLS }}>
                  <span>{statusTag(s)}</span>
                  <span className="mono">block {int(s.height)}</span>
                  <span>{s.hash ? <TxHash hash={s.hash} /> : <span className="faint">—</span>}</span>
                  <span className="muted">{s.reason ?? ""}</span>
                </div>
              ))}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

export default function Mine() {
  const [key, setKey] = useState<Key | null>(() => loadKey()?.key ?? null);
  const [account, setAccount] = useState<MineAccount | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [tick, setTick] = useState(0);

  useEffect(() => {
    if (!key) return;
    let cancelled = false;
    fetchAccount(key.address)
      .then((a) => !cancelled && (setAccount(a), setError(null)))
      .catch((e) => !cancelled && setError(e instanceof Error ? e.message : String(e)));
    return () => {
      cancelled = true;
    };
  }, [key, tick]);

  const refresh = useMemo(() => () => setTick((t) => t + 1), []);

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1 className="page-title">Mine</h1>
          <div className="page-sub">
            Native scrypt mining on the Aether testnet, from this browser. Testnet AETH has no market value. A miner with enough
            of the work and its own node becomes one of the <Link to="/validators">validators</Link>.
          </div>
        </div>
      </div>
      <div className="mine-body">
        {error && <div className="error-banner">{error}</div>}
        {!key ? (
          <KeySetup onReady={(k) => setKey(k)} />
        ) : !account ? (
          <div className="card card-pad muted">Reading your miner account…</div>
        ) : !account.exists ? (
          <Activate address={key.address} onDone={refresh} />
        ) : (
          <MinerPanel
            keyObj={key}
            account={account}
            onRefresh={refresh}
            onForget={() => {
              if (window.confirm("Forget this miner account in this browser? Without its recovery phrase it can't be recovered.")) {
                forgetKey();
                setKey(null);
                setAccount(null);
              }
            }}
          />
        )}
      </div>
    </div>
  );
}
