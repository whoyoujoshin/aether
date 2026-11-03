import { FormEvent, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { buildSend, Key } from "../mine/chain";

type Offer = {
  name: string;
  description?: string;
  url: string;
  price: string;
  amount?: string;
  asset?: string;
  payTo?: string;
  job?: boolean;
  requirements?: Record<string, unknown>;
};

type Receipt = {
  name: string;
  price: string;
  hash: string;
  at: number;
};

type View = "search" | "confirm" | "approve" | "receipt" | "error" | "setup";

const CAP_UAETH = 10_000; // 0.01 AETH
const DAILY_UAETH = 100_000; // 0.1 AETH
const STORE = "aether-shop-v1";

const DEMO: Record<string, { offers?: Offer[]; view?: View; error?: string; receipt?: Receipt; spent?: number }> = {
  empty: { offers: [] },
  results: {
    offers: [
      { name: "Chain pulse", description: "Height, block time, and difficulty", url: "https://example.test/svc/pulse", price: "0.0005 AETH", amount: "500" },
      { name: "Miner leaderboard", description: "Who mined this epoch", url: "https://example.test/svc/miners", price: "0.0005 AETH", amount: "500" },
      { name: "Name a cat", description: "Three names for a grey cat", url: "", price: "0.002 AETH", job: true },
    ],
  },
  approve: {
    view: "approve",
        offers: [{ name: "Hello (USDC)", description: "A practice purchase in test USDC", url: "https://example.test/svc/hello-usdc", price: "0.001 USDC", amount: "1000" }],
  },
  receipt: {
    view: "receipt",
    receipt: { name: "Chain pulse", price: "0.0005 AETH", hash: "AB12CD34EF56AB12CD34EF56AB12CD34EF56AB12CD34EF56AB12CD34EF56AB12", at: Date.now() },
  },
  error: { view: "error", error: "That costs more than your 0.01 AETH limit. Nothing was spent." },
};

function aeth(uaeth: number) {
  const whole = uaeth / 1_000_000;
  return whole.toFixed(6).replace(/0+$/, "").replace(/\.$/, "") + " AETH";
}

function loadSpent(): number {
  try {
    const raw = localStorage.getItem(STORE + "-receipts");
    const rows = raw ? (JSON.parse(raw) as Receipt[]) : [];
    const day = Date.now() - 24 * 60 * 60 * 1000;
    return rows.filter((r) => r.at >= day).reduce((n, r) => n + priceUaeth(r.price), 0);
  } catch {
    return 0;
  }
}

function priceUaeth(price: string) {
  const n = Number(price.split(" ")[0]);
  if (!Number.isFinite(n)) return 0;
  return Math.round(n * 1_000_000);
}

function bytesToBase64(bytes: Uint8Array) {
  let s = "";
  for (const b of bytes) s += String.fromCharCode(b);
  return btoa(s);
}

export default function Shop() {
  const [params] = useSearchParams();
  const demo = params.get("demo") || "";
  const fixture = DEMO[demo];
  const [query, setQuery] = useState(demo === "results" || demo === "approve" ? "block" : "");
  const [offers, setOffers] = useState<Offer[]>(fixture?.offers || []);
  const [view, setView] = useState<View>(fixture?.view || "search");
  const [picked, setPicked] = useState<Offer | null>(fixture?.view === "approve" ? fixture.offers![0] : null);
  const [error, setError] = useState(fixture?.error || "");
  const [receipt, setReceipt] = useState<Receipt | null>(fixture?.receipt || null);
  const [advanced, setAdvanced] = useState(false);
  const [busy, setBusy] = useState(false);
  const [phrase, setPhrase] = useState<string | null>(null);
  const spent = useMemo(() => (demo ? fixture?.spent || (demo === "receipt" ? 500 : 0) : loadSpent()), [demo, fixture]);

  async function onSearch(e: FormEvent) {
    e.preventDefault();
    if (demo) return;
    setError("");
    setView("search");
    setBusy(true);
    try {
      const res = await fetch("/api/shop/search?q=" + encodeURIComponent(query.trim()));
      const body = await res.json();
      if (!res.ok) throw new Error(body.message || body.error || "Search failed.");
      setOffers(body.services || []);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Search failed. Nothing was spent.");
      setView("error");
    } finally {
      setBusy(false);
    }
  }

  function choose(o: Offer) {
    setError("");
    if (o.job) return;
    const cost = o.amount ? Number(o.amount) : priceUaeth(o.price);
    if (cost > CAP_UAETH) {
      setError(`That costs more than your ${aeth(CAP_UAETH)} limit. Nothing was spent.`);
      setView("error");
      return;
    }
    if (spent + cost > DAILY_UAETH) {
      setError("You've reached today's limit. Nothing was spent.");
      setView("error");
      return;
    }
    setPicked(o);
    setView(o.price.includes("USDC") ? "approve" : "confirm");
  }

  async function confirm() {
    if (!picked || demo) {
      if (demo && picked) {
        setReceipt({ name: picked.name, price: picked.price, hash: "AB12CD34".repeat(8), at: Date.now() });
        setView("receipt");
      }
      return;
    }
    const saved = localStorage.getItem(STORE + "-key");
    if (!saved) {
      setView("setup");
      return;
    }
    setBusy(true);
    try {
      const priceRes = await fetch("/api/shop/price?url=" + encodeURIComponent(picked.url));
      const price = await priceRes.json();
      if (!priceRes.ok || price.code === "PRICE_CHANGED" || price.code === "NOT_LISTED") {
        throw new Error(price.message || "The price changed. Look again before buying.");
      }
      const cost = Number(price.amount);
      if (cost > CAP_UAETH || spent + cost > DAILY_UAETH) {
        throw new Error(cost > CAP_UAETH ? `That costs more than your ${aeth(CAP_UAETH)} limit. Nothing was spent.` : "You've reached today's limit. Nothing was spent.");
      }
      const key = Key.fromMnemonic(JSON.parse(saved).mnemonic);
      const acct = await fetch("/api/mine/account?addr=" + encodeURIComponent(key.address)).then((r) => r.json());
      if (!acct.exists) throw new Error("This wallet has no test coins yet.");
      const signed = buildSend(key, {
        chainId: "aether-testnet-1",
        accountNumber: BigInt(acct.accountNumber),
        sequence: BigInt(acct.sequence),
        to: price.payTo,
        amountUaeth: BigInt(price.amount),
        denom: price.asset,
        gasLimit: 400_000n,
      });
      const payload = {
        x402Version: 2,
        accepted: price.requirements,
        payload: { transaction: bytesToBase64(signed.txBytes) },
      };
      const settled = await fetch("/facilitator/settle", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ x402Version: 2, paymentPayload: payload, paymentRequirements: price.requirements }),
      }).then((r) => r.json());
      if (!settled.success) throw new Error("The payment did not go through. Nothing new was signed.");
      const rec: Receipt = { name: picked.name, price: price.price || picked.price, hash: settled.transaction || signed.hash, at: Date.now() };
      const prev = JSON.parse(localStorage.getItem(STORE + "-receipts") || "[]") as Receipt[];
      localStorage.setItem(STORE + "-receipts", JSON.stringify([rec, ...prev].slice(0, 50)));
      setReceipt(rec);
      setView("receipt");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Something went wrong. Nothing was spent.");
      setView("error");
    } finally {
      setBusy(false);
    }
  }

  function createWallet() {
    const fresh = Key.generate();
    localStorage.setItem(STORE + "-key", JSON.stringify({ mnemonic: fresh.mnemonic, address: fresh.key.address }));
    setPhrase(fresh.mnemonic);
  }

  return (
    <div className="page shop">
      <h1 className="page-title">Shop</h1>
      <p className="shop-cap">
        <span>Today's spending <strong>{aeth(spent)}</strong> of {aeth(DAILY_UAETH)}</span>
        <span className="shop-dot">·</span>
        <span>Limit per buy <strong>{aeth(CAP_UAETH)}</strong></span>
      </p>

      {view === "setup" || phrase ? (
        <section className="card card-pad">
          <h2>Create a wallet in this browser</h2>
          <p className="muted">Save these words. Anyone who can use this browser can spend the test coins in it.</p>
          {phrase && <p className="shop-phrase">{phrase}</p>}
          {!phrase && <button className="btn-red shop-btn" type="button" onClick={createWallet}>Create a wallet</button>}
          <p><Link to="/faucet">Get test coins</Link></p>
        </section>
      ) : null}

      {view !== "receipt" && view !== "setup" && (
        <form className="shop-search" onSubmit={onSearch}>
          <label className="shop-label" htmlFor="shop-q">What do you need?</label>
          <div className="shop-search-row">
            <input id="shop-q" value={query} onChange={(e) => setQuery(e.target.value)} placeholder="block, miner, hello" />
            <button className="btn-red" type="submit" disabled={busy}>{busy ? "Looking…" : "Find"}</button>
          </div>
        </form>
      )}

      {view === "search" && offers.length === 0 && (query === "" || demo === "empty") && (
        <p className="muted">Try block, miner, or hello.</p>
      )}
      {view === "search" && offers.length === 0 && query !== "" && demo !== "empty" && (
        <p>Nothing matched that. Try fewer words.</p>
      )}

      {view === "search" && offers.map((o) => (
        <article className="card card-pad shop-offer" key={o.name + o.price}>
          <div>
            <h2>{o.name}</h2>
            {o.description && <p className="muted">{o.description}</p>}
          </div>
          <div className="shop-buy">
            <div className="shop-price">{o.price}</div>
            {o.job ? <p className="muted">Custom job, not ready to buy yet</p> : <button className="btn-red shop-btn" type="button" onClick={() => choose(o)}>Buy</button>}
          </div>
        </article>
      ))}

      {(view === "confirm" || view === "approve") && picked && (
        <section className="card card-pad">
          <h2>{view === "approve" ? "Allow this buy?" : `Buy ${picked.name}?`}</h2>
          <p className="shop-price">{picked.price}</p>
          <p className="muted">{view === "approve" ? "This one is priced in test USDC. Approve it to continue." : "This stays inside today's limit."}</p>
          <div className="shop-actions">
            <button className="btn-red shop-btn" type="button" onClick={confirm} disabled={busy}>{view === "approve" ? "Approve" : "Buy"}</button>
            <button className="btn-outline shop-btn" type="button" onClick={() => setView("search")}>Cancel</button>
          </div>
        </section>
      )}

      {view === "receipt" && receipt && (
        <section className="card card-pad">
          <h2>Paid {receipt.price} for {receipt.name}</h2>
          <p>Payment <span className="mono">{receipt.hash.slice(0, 10)}…</span></p>
          <p><Link to={"/tx/" + receipt.hash}>View on explorer</Link></p>
        </section>
      )}

      {view === "error" && <p className="shop-error" role="alert">{error}</p>}

      <button className="shop-advanced" type="button" onClick={() => setAdvanced((v) => !v)}>
        {advanced ? "Hide details" : "Advanced"}
      </button>
      {advanced && picked && (
        <p className="mono shop-detail">Paid to {picked.payTo || "(the service's account)"} · {picked.amount || ""} {picked.asset || ""}</p>
      )}
    </div>
  );
}
