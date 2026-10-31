import { useEffect, useMemo, useRef, useState } from "react";
import { useServerFn } from "@tanstack/react-start";
import { getAddressBalance, getChainStatus, type AddressBalance, type ChainStatus } from "@/lib/chain";
import { WORDS } from "./wordlist";
import type { MinerTick } from "./miner.worker";
import {
  clearWallet,
  createMnemonic,
  downloadPhrase,
  fingerprint,
  loadWallet,
  mnemonicValid,
  saveWallet,
} from "./wallet";

type Phase = "welcome" | "install" | "choice" | "reveal" | "confirm" | "restore" | "connect" | "mine";
type Effort = "easy" | "half" | "full";
type InstallPrompt = Event & {
  prompt: () => Promise<void>;
  userChoice: Promise<{ outcome: "accepted" | "dismissed" }>;
};

const JOURNEY = [
  { id: "setup", label: "Set up", hint: "This computer" },
  { id: "wallet", label: "Wallet", hint: "12 words" },
  { id: "link", label: "Connect", hint: "Testnet" },
  { id: "mine", label: "Mine", hint: "Your processor" },
] as const;

const INSTALL_LINES = [
  "Looking at this computer",
  "Preparing the miner",
  "Making a private place for your words",
  "Ready to open",
];

const EXPLORER = "https://explorer.157-245-252-221.sslip.io";
const WATCH_KEY = "aether-miner-watch-v1";

function journeyIndex(phase: Phase): number {
  if (phase === "welcome" || phase === "install") return 0;
  if (phase === "choice" || phase === "reveal" || phase === "confirm" || phase === "restore") return 1;
  if (phase === "connect") return 2;
  return 3;
}

function shuffle<T>(items: T[]): T[] {
  const copy = [...items];
  for (let i = copy.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    const a = copy[i] as T;
    copy[i] = copy[j] as T;
    copy[j] = a;
  }
  return copy;
}

function workerCount(effort: Effort, cores: number): number {
  const n = Math.max(1, cores);
  if (effort === "easy") return 1;
  if (effort === "half") return Math.max(1, Math.floor(n / 2));
  return n;
}

function formatRate(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return "0";
  if (n < 10) return n.toFixed(1);
  return Math.round(n).toLocaleString();
}

function formatAeth(raw: string): string {
  const digits = raw.trim().match(/^(\d+)/)?.[1] ?? "0";
  const n = BigInt(digits);
  const whole = n / 1_000_000n;
  const frac = (n % 1_000_000n).toString().padStart(6, "0").replace(/0+$/, "");
  return frac ? `${whole.toLocaleString()}.${frac}` : whole.toLocaleString();
}

function txWhen(iso: string): string {
  const t = Date.parse(iso);
  if (!Number.isFinite(t)) return "";
  return new Date(t).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  });
}

function txLabel(direction: string, failed: boolean): string {
  if (failed) return "Failed";
  if (direction === "received") return "Received";
  if (direction === "sent") return "Sent";
  if (!direction) return "Transfer";
  return direction.charAt(0).toUpperCase() + direction.slice(1);
}

function blockAge(iso: string, nowMs: number): string {
  const t = Date.parse(iso);
  if (!Number.isFinite(t)) return "waiting";
  const s = Math.max(0, Math.round((nowMs - t) / 1000));
  if (s < 5) return "just now";
  if (s < 90) return `${s} seconds ago`;
  const m = Math.round(s / 60);
  return m === 1 ? "1 minute ago" : `${m} minutes ago`;
}

function Mark() {
  return (
    <svg className="mark" viewBox="0 0 32 32" aria-hidden="true">
      <rect width="32" height="32" rx="7" fill="#1e1a16" />
      <g transform="translate(5 5) scale(0.22)">
        <polygon points="26 6 46 6 24 94 4 94" fill="#f2eee6" />
        <polygon points="50 6 66 6 82 94 66 94" fill="#f2eee6" />
        <rect x="26" y="6" width="68" height="17" fill="#f2eee6" />
        <rect x="62" y="77" width="32" height="17" fill="#f2eee6" />
        <rect x="18" y="42" width="72" height="17" fill="#c74d38" />
      </g>
    </svg>
  );
}

export function MinerApp() {
  const fetchStatus = useServerFn(getChainStatus);
  const lookupBalance = useServerFn(getAddressBalance);
  const [booted, setBooted] = useState(false);
  const [phase, setPhase] = useState<Phase>("welcome");
  const [installAt, setInstallAt] = useState(0);
  const [phrase, setPhrase] = useState("");
  const [restoreText, setRestoreText] = useState("");
  const [restoreError, setRestoreError] = useState("");
  const [checks, setChecks] = useState<Record<number, string>>({});
  const [copied, setCopied] = useState(false);
  const [busy, setBusy] = useState(false);
  const [chain, setChain] = useState<ChainStatus | null>(null);
  const [connectTry, setConnectTry] = useState(0);
  const [minerId, setMinerId] = useState("");
  const [mining, setMining] = useState(false);
  const [effort, setEffort] = useState<Effort>("half");
  const [hashrate, setHashrate] = useState(0);
  const [shares, setShares] = useState(0);
  const [hashes, setHashes] = useState(0);
  const [stream, setStream] = useState<string[]>([]);
  const [hitCore, setHitCore] = useState(-1);
  const [showPhrase, setShowPhrase] = useState(false);
  const [wiping, setWiping] = useState(false);
  const [showBalance, setShowBalance] = useState(false);
  const [watch, setWatch] = useState("");
  const [balance, setBalance] = useState<AddressBalance | null>(null);
  const [looking, setLooking] = useState(false);
  const [now, setNow] = useState(() => Date.now());
  const [heightTick, setHeightTick] = useState(false);
  const [installEvent, setInstallEvent] = useState<InstallPrompt | null>(null);
  const [pinned, setPinned] = useState(false);
  const [coreTotal, setCoreTotal] = useState(2);
  const workers = useRef<Worker[]>([]);
  const life = useRef({ hashes: 0, shares: 0 });
  const chainRef = useRef(chain);
  chainRef.current = chain;
  const prevHeight = useRef("");

  const words = phrase ? phrase.split(" ") : [];
  const at = journeyIndex(phase);
  const usedCores = workerCount(effort, coreTotal);
  const quiz = useMemo(() => {
    const list = phrase ? phrase.split(" ") : [];
    if (list.length !== 12) return [];
    const spots = shuffle([0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11]).slice(0, 3).sort((a, b) => a - b);
    return spots.map((index) => {
      const answer = list[index] ?? "";
      const decoys = shuffle(WORDS.filter((w) => w !== answer)).slice(0, 3);
      return { index, answer, options: shuffle([answer, ...decoys]) };
    });
  }, [phrase]);
  const quizReady = quiz.length === 3 && quiz.every((q) => checks[q.index] === q.answer);
  const quizMiss = quiz.some((q) => checks[q.index] && checks[q.index] !== q.answer);

  useEffect(() => {
    const saved = loadWallet();
    if (saved) {
      setPhrase(saved.mnemonic);
      void fingerprint(saved.mnemonic).then(setMinerId);
      setPhase("connect");
    }
    setCoreTotal(navigator.hardwareConcurrency || 2);
    try {
      setWatch(localStorage.getItem(WATCH_KEY) ?? "");
    } catch {
      setWatch("");
    }
    setBooted(true);
    const onPrompt = (event: Event) => {
      event.preventDefault();
      setInstallEvent(event as InstallPrompt);
    };
    window.addEventListener("beforeinstallprompt", onPrompt);
    setPinned(window.matchMedia("(display-mode: standalone)").matches);
    return () => window.removeEventListener("beforeinstallprompt", onPrompt);
  }, []);

  useEffect(() => {
    if (phase !== "install") return;
    setInstallAt(0);
    const timers = INSTALL_LINES.map((_, i) => window.setTimeout(() => setInstallAt(i + 1), 650 * (i + 1)));
    const done = window.setTimeout(() => setPhase("choice"), 650 * INSTALL_LINES.length + 500);
    return () => {
      timers.forEach((id) => window.clearTimeout(id));
      window.clearTimeout(done);
    };
  }, [phase]);

  useEffect(() => {
    if (phase !== "connect") return;
    let cancelled = false;
    let timer = 0;
    setChain(null);
    void fetchStatus().then((status) => {
      if (cancelled) return;
      setChain(status);
      if (status.ok) {
        timer = window.setTimeout(() => {
          if (!cancelled) setPhase("mine");
        }, 1400);
      }
    });
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [phase, connectTry, fetchStatus]);

  useEffect(() => {
    if (phase !== "mine" && phase !== "connect") return;
    const id = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, [phase]);

  useEffect(() => {
    if (phase !== "mine") return;
    const id = window.setInterval(() => {
      void fetchStatus().then(setChain);
    }, 6000);
    return () => window.clearInterval(id);
  }, [phase, fetchStatus]);

  useEffect(() => {
    if (!chain?.ok) return;
    if (prevHeight.current && prevHeight.current !== chain.height) {
      setHeightTick(true);
      const id = window.setTimeout(() => setHeightTick(false), 450);
      prevHeight.current = chain.height;
      return () => window.clearTimeout(id);
    }
    prevHeight.current = chain.height;
  }, [chain]);

  useEffect(() => {
    if (!mining) {
      workers.current.forEach((worker) => {
        worker.postMessage({ type: "stop" });
        worker.terminate();
      });
      workers.current = [];
      return;
    }
    const count = workerCount(effort, navigator.hardwareConcurrency || coreTotal);
    const seed = crypto.getRandomValues(new Uint8Array(36));
    const linked = chainRef.current;
    if (linked?.ok) {
      const height = Number(linked.height) || 0;
      seed[0] = height & 0xff;
      seed[1] = (height >> 8) & 0xff;
    }
    const windowStat = { hashes: 0, ms: 0 };
    let stop = false;
    const next: Worker[] = [];
    for (let i = 0; i < count; i++) {
      const worker = new Worker(new URL("./miner.worker.ts", import.meta.url), { type: "module" });
      worker.onmessage = (event: MessageEvent<MinerTick>) => {
        if (stop) return;
        const tick = event.data;
        life.current.hashes += tick.hashes;
        life.current.shares += tick.shares;
        windowStat.hashes += tick.hashes;
        windowStat.ms += tick.ms;
        const seconds = windowStat.ms / 1000 / count;
        setHashes(life.current.hashes);
        setShares(life.current.shares);
        setHashrate(seconds > 0 ? windowStat.hashes / seconds : 0);
        if (tick.last) setStream((prev) => [tick.last, ...prev].slice(0, 4));
        if (tick.shares > 0) {
          setHitCore(i);
          window.setTimeout(() => setHitCore((cur) => (cur === i ? -1 : cur)), 700);
        }
      };
      worker.postMessage({ type: "start", header: [...seed, 0, 0, 0, 0], nonce: (i * 0x1000000) >>> 0 });
      next.push(worker);
    }
    workers.current = next;
    return () => {
      stop = true;
      next.forEach((worker) => {
        worker.postMessage({ type: "stop" });
        worker.terminate();
      });
    };
  }, [mining, effort, coreTotal]);

  async function finishCreate() {
    if (busy) return;
    setBusy(true);
    try {
      const made = await createMnemonic();
      setPhrase(made);
      setChecks({});
      setMinerId(await fingerprint(made));
      setCopied(false);
      setPhase("reveal");
    } finally {
      setBusy(false);
    }
  }

  function acceptPhrase() {
    saveWallet(phrase);
    setPhase("connect");
  }

  async function restore() {
    const cleaned = restoreText.trim().toLowerCase().replace(/[^a-z\s]/g, " ").replace(/\s+/g, " ");
    if (!(await mnemonicValid(cleaned))) {
      setRestoreError("Those are not 12 recovery words. Check the spelling and the order, then try again.");
      return;
    }
    setRestoreError("");
    setPhrase(cleaned);
    setMinerId(await fingerprint(cleaned));
    saveWallet(cleaned);
    setPhase("connect");
  }

  async function copyWords() {
    try {
      await navigator.clipboard.writeText(phrase);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1600);
    } catch {
      downloadPhrase(phrase);
    }
  }

  async function pinApp() {
    if (!installEvent) return;
    await installEvent.prompt();
    const choice = await installEvent.userChoice;
    if (choice.outcome === "accepted") setPinned(true);
    setInstallEvent(null);
  }

  async function lookup(address = watch) {
    setLooking(true);
    try {
      const result = await lookupBalance({ data: address.trim() });
      setBalance(result);
      if (result.ok) {
        localStorage.setItem(WATCH_KEY, result.address);
        setWatch(result.address);
      }
    } catch {
      setBalance({ ok: false, error: "Could not reach the Aether explorer. Try again in a moment." });
    } finally {
      setLooking(false);
    }
  }

  function openBalance() {
    setShowBalance(true);
    if (watch && !balance) void lookup(watch);
  }

  function resetAll() {
    setMining(false);
    clearWallet();
    life.current = { hashes: 0, shares: 0 };
    setPhrase("");
    setMinerId("");
    setShares(0);
    setHashes(0);
    setHashrate(0);
    setStream([]);
    setShowPhrase(false);
    setWiping(false);
    setChain(null);
    setPhase("welcome");
  }

  const installPct = Math.round((installAt / INSTALL_LINES.length) * 100);
  const ringC = 2 * Math.PI * 58;
  const linked = chain?.ok === true;
  const statusLabel = mining ? "Mining" : linked ? "Linked" : "Testnet";

  return (
    <div className="app-shell">
      <div className="shell">
        <aside className="rail">
          <div className="brand">
            <Mark />
            <div>
              <strong>Aether</strong>
              <em>Miner</em>
            </div>
          </div>
          <ol className="journey">
            {JOURNEY.map((step, i) => (
              <li key={step.id} className={i < at ? "done" : i === at ? "now" : ""}>
                <span>{i < at ? "✓" : i + 1}</span>
                <div>
                  <b>{step.label}</b>
                  <small>{step.hint}</small>
                </div>
              </li>
            ))}
          </ol>
          <div className="rail-end">
            {at === 3 && (
              <button className="rail-btn" type="button" onClick={() => (showBalance ? setShowBalance(false) : openBalance())}>
                {showBalance ? "Back to mining" : "Balance"}
              </button>
            )}
            <p className="rail-foot">
              <span className={mining ? "dot busy" : linked ? "dot live" : "dot"} />
              {statusLabel}
            </p>
          </div>
        </aside>
        <div className="stage" key={booted ? phase : "boot"}>
          {!booted && (
            <>
              <h1>Opening Aether Miner.</h1>
              <p className="lede">One moment.</p>
            </>
          )}

          {booted && phase === "welcome" && (
            <>
              <h1>Mine Aether on this computer.</h1>
              <p className="lede">
                Setup takes about a minute. You get 12 recovery words, then this window links the public testnet and puts your processor to work. Nothing to type at a command line.
              </p>
              <div className="row">
                <button className="btn" type="button" onClick={() => setPhase("install")}>
                  Start setup
                </button>
                {installEvent && !pinned && (
                  <button className="btn ghost" type="button" onClick={() => void pinApp()}>
                    Pin to this computer
                  </button>
                )}
                {pinned && <span className="muted">Pinned on this computer.</span>}
              </div>
            </>
          )}

          {booted && phase === "install" && (
            <>
              <h1>Setting up.</h1>
              <p className="lede">
                Found {coreTotal} processor {coreTotal === 1 ? "core" : "cores"}. This stays in the window. Your recovery words are saved only in this browser.
              </p>
              <div className="setup">
                <div className="ring" aria-hidden="true">
                  <svg viewBox="0 0 148 148">
                    <circle className="track" cx="74" cy="74" r="58" />
                    <circle
                      className="fill"
                      cx="74"
                      cy="74"
                      r="58"
                      strokeDasharray={ringC}
                      strokeDashoffset={ringC * (1 - installAt / INSTALL_LINES.length)}
                    />
                  </svg>
                  <strong>{installPct}%</strong>
                </div>
                <div className="steps">
                  {INSTALL_LINES.map((line, i) => {
                    const state = installAt > i + 1 ? "done" : installAt === i + 1 ? "run" : "";
                    return (
                      <div className={`step ${state} ${installAt > i ? "on" : ""}`} key={line}>
                        <span>{installAt > i + 1 ? "✓" : i + 1}</span>
                        <div>
                          {line}
                          <div className="bar">
                            <i />
                          </div>
                        </div>
                      </div>
                    );
                  })}
                </div>
              </div>
            </>
          )}

          {booted && phase === "choice" && (
            <>
              <h1>Create your miner wallet.</h1>
              <p className="lede">
                Twelve words bring this miner back if you clear the browser. Write them on paper. They are not a key that can spend Aether.
              </p>
              <div className="row">
                <button className="btn" type="button" disabled={busy} onClick={() => void finishCreate()}>
                  {busy ? "Creating…" : "Create wallet"}
                </button>
                <button className="btn ghost" type="button" onClick={() => setPhase("restore")}>
                  I already have words
                </button>
              </div>
            </>
          )}

          {booted && phase === "reveal" && (
            <>
              <h1>Write these words down.</h1>
              <div className="warn">
                Anyone with these words can restore this miner. Keep them offline. They cannot move coins on the chain.
              </div>
              <div className="words">
                {words.map((word, i) => (
                  <div className="word" key={`${word}-${i}`}>
                    <b>{i + 1}</b>
                    <span>{word}</span>
                  </div>
                ))}
              </div>
              <div className="row">
                <button className="btn" type="button" onClick={() => setPhase("confirm")}>
                  I wrote them down
                </button>
                <button className="btn ghost" type="button" onClick={() => void copyWords()}>
                  {copied ? "Copied" : "Copy words"}
                </button>
                <button className="btn ghost" type="button" onClick={() => downloadPhrase(phrase)}>
                  Save a text file
                </button>
              </div>
            </>
          )}

          {booted && phase === "confirm" && (
            <>
              <h1>Check three words.</h1>
              <p className="lede">Tap the word that belongs in each spot. This makes sure the phrase was saved.</p>
              <div className="quiz">
                {quiz.map((q) => (
                  <div key={q.index}>
                    <div className="muted">Word {q.index + 1}</div>
                    <div className="choices">
                      {q.options.map((option) => {
                        const picked = checks[q.index] === option;
                        const good = picked && option === q.answer;
                        const bad = picked && option !== q.answer;
                        return (
                          <button
                            key={option}
                            type="button"
                            className={good ? "good" : bad ? "pick" : ""}
                            onClick={() => setChecks((prev) => ({ ...prev, [q.index]: option }))}
                          >
                            {option}
                          </button>
                        );
                      })}
                    </div>
                  </div>
                ))}
              </div>
              {quizMiss && <p className="hint">One of those is not right. Try another word.</p>}
              <button className="btn" type="button" disabled={!quizReady} onClick={acceptPhrase}>
                Continue
              </button>
            </>
          )}

          {booted && phase === "restore" && (
            <>
              <h1>Restore from your words.</h1>
              <p className="lede">Paste the 12 words, in order, with a space between each one.</p>
              <textarea
                className="field"
                value={restoreText}
                onChange={(e) => {
                  setRestoreText(e.target.value);
                  setRestoreError("");
                }}
                placeholder="word word word …"
              />
              {restoreError && <p className="error">{restoreError}</p>}
              <div className="row">
                <button className="btn" type="button" onClick={() => void restore()}>
                  Restore
                </button>
                <button className="btn ghost" type="button" onClick={() => setPhase("choice")}>
                  Back
                </button>
              </div>
            </>
          )}

          {booted && phase === "connect" && (
            <>
              <div className={`orbit ${linked ? "linked" : ""}`} aria-hidden="true">
                <i />
                <i />
                <i />
                <b>{linked ? "Linked" : chain ? "Quiet" : "Linking"}</b>
              </div>
              <h1>{linked ? "Connected to the testnet." : chain ? "The testnet did not answer." : "Connecting…"}</h1>
              <p className="lede">
                {linked
                  ? `${chain.network} is at block ${Number(chain.height).toLocaleString()}. Last block ${blockAge(chain.blockTime, now)}.`
                  : chain
                    ? chain.error
                    : "Asking the public testnet for the latest block."}
              </p>
              {chain && !chain.ok && (
                <div className="row">
                  <button className="btn" type="button" onClick={() => setConnectTry((n) => n + 1)}>
                    Try again
                  </button>
                  <button className="btn ghost" type="button" onClick={() => setPhase("mine")}>
                    Continue anyway
                  </button>
                </div>
              )}
            </>
          )}

          {booted && phase === "mine" && showBalance && (
            <>
              <h1>Balance</h1>
              <p className="lede">
                Mining in this window does not add AETH. A chain balance belongs to an aether1 address.
              </p>
              <div className="dash">
                <div className="panel">
                  <div className="muted">From this miner</div>
                  <div className="stat">0</div>
                  <div className="stat-label">AETH earned</div>
                  <p className="note">
                    Practice finds so far: {shares.toLocaleString()}. They stay on this computer. They are not paid onto the testnet.
                  </p>
                </div>
                <div className="panel">
                  <div className="muted">Wallet on the testnet</div>
                  <input
                    className="addr"
                    value={watch}
                    onChange={(e) => setWatch(e.target.value)}
                    placeholder="aether1…"
                    spellCheck={false}
                    autoCapitalize="none"
                    aria-label="Aether address"
                  />
                  <div className="row">
                    <button className="btn" type="button" disabled={looking || !watch.trim()} onClick={() => void lookup()}>
                      {looking ? "Looking up…" : "Look up balance"}
                    </button>
                  </div>
                  {balance?.ok === false && <p className="error">{balance.error}</p>}
                  {balance?.ok && (
                    <>
                      <div className="stat" style={{ marginTop: 12 }}>
                        {formatAeth(balance.uaeth)}
                      </div>
                      <div className="stat-label">AETH</div>
                      {balance.txs.length > 0 && (
                        <ul className="txs">
                          {balance.txs.map((tx) => (
                            <li key={tx.hash || `${tx.timestamp}-${tx.amount}`}>
                              <span>
                                {txLabel(tx.direction, tx.failed)}
                                <small>{txWhen(tx.timestamp)}</small>
                              </span>
                              <b>{formatAeth(tx.amount)}</b>
                            </li>
                          ))}
                        </ul>
                      )}
                      <a href={`${EXPLORER}/address/${balance.address}`} target="_blank" rel="noreferrer">
                        Open this address on the explorer
                      </a>
                    </>
                  )}
                </div>
              </div>
              <button className="btn ghost" type="button" style={{ marginTop: 16 }} onClick={() => setShowBalance(false)}>
                Back to mining
              </button>
            </>
          )}

          {booted && phase === "mine" && !showBalance && (
            <>
              <h1>{mining ? "Your computer is mining." : "Ready when you are."}</h1>
              <p className="lede">
                {minerId ? `This miner is ${minerId}. ` : ""}
                {mining
                  ? "The processor is searching Scrypt hashes. The block number updates as the testnet moves."
                  : "Press start and this computer searches Scrypt hashes. The testnet stays on screen so you can see the chain move."}
              </p>
              <div className="row" style={{ marginTop: -8, marginBottom: 18 }}>
                <button className="btn" type="button" onClick={openBalance}>
                  Balance
                </button>
              </div>
              <div className="dash">
                <div className="panel">
                  <div className="muted">Tries each second</div>
                  <div className="stat">{mining ? formatRate(hashrate) : "Paused"}</div>
                  <div className="stat-label">
                    {mining
                      ? `Using ${usedCores} of ${coreTotal} cores`
                      : `${coreTotal} cores available`}
                  </div>
                  <div className={`cores ${mining ? "live" : ""}`} aria-hidden="true">
                    {Array.from({ length: Math.min(coreTotal, 16) }, (_, i) => (
                      <i
                        key={i}
                        className={i >= usedCores ? "off" : hitCore === i ? "hit" : "on"}
                        style={i < usedCores ? { animationDuration: `${0.7 + (i % 5) * 0.08}s` } : undefined}
                      />
                    ))}
                  </div>
                  <div className="meter" aria-hidden="true">
                    {mining && <i />}
                  </div>
                  <div className="stream">
                    {(stream.length ? stream : ["waiting to start"]).map((line, i) => (
                      <span key={`${line}-${i}`}>{line}</span>
                    ))}
                  </div>
                  <div className="muted" style={{ marginTop: 14 }}>
                    How hard should this computer work?
                  </div>
                  <div className="levels">
                    {(
                      [
                        ["easy", "Gentle", "One core"],
                        ["half", "Medium", "Half the cores"],
                        ["full", "All cores", "Fan may spin up"],
                      ] as const
                    ).map(([id, label, hint]) => (
                      <button
                        key={id}
                        type="button"
                        className={effort === id ? "on" : ""}
                        aria-pressed={effort === id}
                        onClick={() => setEffort(id)}
                      >
                        <b>{label}</b>
                        <small>{hint}</small>
                      </button>
                    ))}
                  </div>
                  <button className="btn" type="button" onClick={() => setMining((on) => !on)}>
                    {mining ? "Pause" : "Start mining"}
                  </button>
                </div>
                <div className="panel chain-card">
                  <div className="muted">Public testnet</div>
                  <div className={`stat ${heightTick ? "pop" : ""}`}>
                    {linked ? Number(chain.height).toLocaleString() : "—"}
                  </div>
                  <div className="stat-label">
                    {linked
                      ? `${chain.network}${chain.catchingUp ? " · catching up" : ""} · ${blockAge(chain.blockTime, now)}`
                      : chain && !chain.ok
                        ? "Not linked"
                        : "Checking…"}
                  </div>
                  <div className="muted" style={{ marginTop: 12 }}>
                    Practice finds
                  </div>
                  <div className={`stat finds ${hitCore >= 0 ? "flash" : ""}`}>{shares.toLocaleString()}</div>
                  <div className="muted">{hashes.toLocaleString()} hashes tried</div>
                  <p className="note">
                    A find means this computer hit a Scrypt hash with a leading zero. It stays here. This window cannot sign a block onto the testnet. That needs the Aether node and an ML-DSA key.
                  </p>
                  <a href={EXPLORER} target="_blank" rel="noreferrer">
                    Open the public explorer
                  </a>
                  <div className="row" style={{ marginTop: 14 }}>
                    <button className="btn ghost" type="button" onClick={() => setShowPhrase((v) => !v)}>
                      {showPhrase ? "Hide words" : "Show recovery words"}
                    </button>
                  </div>
                  {showPhrase && <p className="note">{phrase}</p>}
                  {wiping ? (
                    <div className="row" style={{ marginTop: 10 }}>
                      <button className="btn" type="button" onClick={resetAll}>
                        Erase words
                      </button>
                      <button className="btn ghost" type="button" onClick={() => setWiping(false)}>
                        Keep them
                      </button>
                    </div>
                  ) : (
                    <button className="btn ghost" type="button" style={{ marginTop: 10 }} onClick={() => setWiping(true)}>
                      Start over on this browser
                    </button>
                  )}
                </div>
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
