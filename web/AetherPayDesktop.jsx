import React, { useState, useEffect, useCallback } from "react";

const ACC = "#2ad4c4";
const GOLD = "#e3b25f";
const API = "http://localhost:8090";

const NAV = [
  ["home", "Home", "◎"],
  ["send", "Send", "↗"],
  ["receive", "Receive", "▣"],
  ["activity", "Activity", "≡"],
  ["faucet", "Faucet", "◈"],
  ["buy", "Buy AETH", "$"],
  ["swap", "Swap", "⇄"],
];

function fmt(n, min = 4) {
  return Number(n).toLocaleString("en-US", { minimumFractionDigits: min, maximumFractionDigits: 6 });
}
function uaethToAeth(uaeth) {
  return Number(uaeth) / 1_000_000;
}
function short(addr) {
  if (!addr) return "";
  return addr.slice(0, 11) + "…" + addr.slice(-6);
}

export default function AetherPayDesktop() {
  const [screen, setScreen] = useState("home");
  const [accounts, setAccounts] = useState([]);
  const [accountName, setAccountName] = useState("");
  const [account, setAccount] = useState(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const [sendTo, setSendTo] = useState("");
  const [sendAmt, setSendAmt] = useState("");
  const [sendStep, setSendStep] = useState(0);
  const [sending, setSending] = useState(false);
  const [sendResult, setSendResult] = useState(null);

  const [activity, setActivity] = useState([]);
  const [activityLoading, setActivityLoading] = useState(false);

  const [faucetState, setFaucetState] = useState("idle");
  const [faucetMsg, setFaucetMsg] = useState("");

  const [copied, setCopied] = useState(false);

  const loadAccounts = useCallback(async () => {
    try {
      const res = await fetch(`${API}/api/accounts`);
      const data = await res.json();
      setAccounts(data || []);
      if (data && data.length > 0 && !accountName) {
        setAccountName(data[0].Name || data[0].name);
      }
    } catch (e) {
      setError("Could not reach the local wallet API at localhost:8090. Is it running? (go run ./cmd/walletapi)");
    }
  }, [accountName]);

  const loadAccount = useCallback(async (name) => {
    if (!name) return;
    setLoading(true);
    setError("");
    try {
      const res = await fetch(`${API}/api/account?name=${encodeURIComponent(name)}`);
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || "failed to load account");
      setAccount(data);
    } catch (e) {
      setError(e.message);
      setAccount(null);
    }
    setLoading(false);
  }, []);

  const loadActivity = useCallback(async (address) => {
    if (!address) return;
    setActivityLoading(true);
    try {
      const res = await fetch(`${API}/api/history?address=${encodeURIComponent(address)}`);
      const data = await res.json();
      setActivity(data || []);
    } catch (e) {
      setActivity([]);
    }
    setActivityLoading(false);
  }, []);

  useEffect(() => { loadAccounts(); }, []);
  useEffect(() => { if (accountName) loadAccount(accountName); }, [accountName]);
  useEffect(() => { if (account?.address && (screen === "activity" || screen === "home")) loadActivity(account.address); }, [account, screen]);

  const balanceAeth = account ? uaethToAeth(account.balance) : 0;
  const validTo = /^aether1[a-z0-9]{38,90}$/.test(sendTo);
  const amtNum = parseFloat(sendAmt) || 0;
  const amtOk = amtNum > 0 && amtNum <= balanceAeth;

  async function doSend() {
    setSending(true);
    try {
      const uaeth = Math.round(amtNum * 1_000_000).toString();
      const res = await fetch(`${API}/api/send`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ from: accountName, to: sendTo, amount: uaeth }),
      });
      const data = await res.json();
      setSendResult(data);
      if (data.success) {
        setSendStep(3);
        setTimeout(() => loadAccount(accountName), 3000);
      }
    } catch (e) {
      setSendResult({ success: false, message: e.message });
    }
    setSending(false);
  }

  async function requestFaucet() {
    if (!account?.address) return;
    setFaucetState("sending");
    try {
      const res = await fetch("http://157.245.252.221:8080/request", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ address: account.address }),
      });
      const data = await res.json();
      if (data.success) {
        setFaucetState("done");
        setFaucetMsg(`Sent. tx ${data.tx_hash?.slice(0, 16)}…`);
        setTimeout(() => loadAccount(accountName), 4000);
      } else {
        setFaucetState("idle");
        setFaucetMsg(data.message || "Request failed");
      }
    } catch (e) {
      setFaucetState("idle");
      setFaucetMsg("Could not reach the public faucet.");
    }
  }

  const wrap = { minHeight: "100vh", background: "#08090a", color: "#f2f4f3", fontFamily: "'IBM Plex Sans', sans-serif", display: "flex" };
  const mono = { fontFamily: "'IBM Plex Mono', monospace" };

  const navBtn = (active) => ({
    display: "flex", alignItems: "center", gap: 11, width: "100%",
    background: active ? "rgba(42,212,196,.09)" : "none",
    border: "none", borderLeft: `2px solid ${active ? ACC : "transparent"}`,
    color: active ? "#f2f4f3" : "#8b9490", padding: "10px 12px",
    borderRadius: "0 9px 9px 0", cursor: "pointer",
    font: `${active ? 600 : 500} 12.5px/1 'IBM Plex Sans',sans-serif`, textAlign: "left",
  });

  const primaryBtn = (enabled) => ({
    background: enabled ? ACC : "#1a1f20", color: enabled ? "#000" : "#7a8481",
    border: "none", borderRadius: 50, padding: "14px 24px",
    font: "600 13px/1 'IBM Plex Sans',sans-serif", cursor: enabled ? "pointer" : "not-allowed", flex: 1,
  });

  const card = { background: "#0d1011", border: "1px solid rgba(255,255,255,.08)", borderRadius: 14, padding: 20 };

  return (
    <div style={wrap}>
      <link rel="preconnect" href="https://fonts.googleapis.com" />
      <link href="https://fonts.googleapis.com/css2?family=IBM+Plex+Mono:wght@400;500;600&family=IBM+Plex+Sans:wght@400;500;600&display=swap" rel="stylesheet" />

      {/* Sidebar */}
      <div style={{ width: 220, borderRight: "1px solid rgba(255,255,255,.08)", padding: "20px 10px", display: "flex", flexDirection: "column", gap: 4 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "0 12px 20px", fontWeight: 700, fontSize: 16, color: ACC }}>
          ⚡ Aether Pay
        </div>
        {NAV.map(([key, label, glyph]) => (
          <button key={key} style={navBtn(screen === key)} onClick={() => setScreen(key)}>
            <span>{glyph}</span> {label}
          </button>
        ))}
        <div style={{ marginTop: "auto", padding: 12 }}>
          <label style={{ ...mono, fontSize: 10, color: "#6b7570" }}>ACCOUNT</label>
          <select
            value={accountName}
            onChange={(e) => setAccountName(e.target.value)}
            style={{ width: "100%", marginTop: 6, background: "#0d1011", color: "#f2f4f3", border: "1px solid rgba(255,255,255,.1)", borderRadius: 8, padding: 8, ...mono, fontSize: 11 }}
          >
            {accounts.map((a) => (
              <option key={a.Name || a.name} value={a.Name || a.name}>{a.Name || a.name}</option>
            ))}
          </select>
        </div>
      </div>

      {/* Main */}
      <div style={{ flex: 1, padding: 32, maxWidth: 700 }}>
        {error && (
          <div style={{ background: "rgba(255,122,107,.08)", border: "1px solid rgba(255,122,107,.3)", borderRadius: 10, padding: 14, marginBottom: 20, color: "#ff9b8f", fontSize: 12.5 }}>
            {error}
          </div>
        )}

        {screen === "home" && (
          <div style={{ display: "flex", flexDirection: "column", gap: 20 }}>
            <div style={card}>
              <div style={{ color: "#8b9490", fontSize: 11.5, marginBottom: 6 }}>BALANCE</div>
              <div style={{ fontSize: 40, fontWeight: 600, ...mono }}>{loading ? "…" : fmt(balanceAeth)} <span style={{ fontSize: 18, color: "#8b9490" }}>AETH</span></div>
              <div style={{ ...mono, fontSize: 10.5, color: "#6b7570", marginTop: 8 }}>
                {account ? `${account.balance} uaeth · ${short(account.address)}` : "no account loaded"}
              </div>
              <div style={{ display: "flex", gap: 10, marginTop: 18 }}>
                <button style={primaryBtn(true)} onClick={() => setScreen("send")}>Send</button>
                <button style={{ ...primaryBtn(true), background: "#1a1f20", color: "#f2f4f3" }} onClick={() => setScreen("receive")}>Receive</button>
              </div>
            </div>

            <div>
              <div style={{ color: "#8b9490", fontSize: 11.5, marginBottom: 10 }}>RECENT ACTIVITY</div>
              {activity.slice(0, 4).map((t) => (
                <TxRow key={t.Hash} t={t} />
              ))}
              {activity.length === 0 && <div style={{ color: "#6b7570", fontSize: 12.5 }}>No transactions yet.</div>}
            </div>
          </div>
        )}

        {screen === "send" && (
          <div style={card}>
            <h2 style={{ marginTop: 0 }}>Send AETH</h2>
            {sendStep < 3 && (
              <>
                <label style={{ ...mono, fontSize: 10.5, color: "#8b9490" }}>RECIPIENT ADDRESS</label>
                <input
                  value={sendTo}
                  onChange={(e) => setSendTo(e.target.value.trim())}
                  placeholder="aether1..."
                  style={{ width: "100%", background: "#0a0c0d", border: "1px solid rgba(255,255,255,.1)", borderRadius: 10, padding: 12, color: "#f2f4f3", ...mono, fontSize: 12.5, marginTop: 6, marginBottom: 4 }}
                />
                <div style={{ fontSize: 10.5, color: sendTo === "" ? "transparent" : validTo ? ACC : "#ff7a6b", ...mono, marginBottom: 14 }}>
                  {sendTo === "" ? "-" : validTo ? "✓ valid aether1 address" : "✕ doesn't match aether1 format"}
                </div>

                <label style={{ ...mono, fontSize: 10.5, color: "#8b9490" }}>AMOUNT (AETH)</label>
                <input
                  value={sendAmt}
                  onChange={(e) => setSendAmt(e.target.value.replace(/[^0-9.]/g, ""))}
                  placeholder="0.00"
                  style={{ width: "100%", background: "#0a0c0d", border: "1px solid rgba(255,255,255,.1)", borderRadius: 10, padding: 12, color: "#f2f4f3", ...mono, fontSize: 12.5, marginTop: 6, marginBottom: 4 }}
                />
                <div style={{ fontSize: 10.5, color: "#6b7570", ...mono, marginBottom: 18 }}>
                  available {fmt(balanceAeth)} AETH · fee 0 uaeth · gas 400,000
                </div>

                <button
                  style={primaryBtn(validTo && amtOk && !sending)}
                  disabled={!(validTo && amtOk) || sending}
                  onClick={doSend}
                >
                  {sending ? "Signing with ML-DSA-44…" : "Sign & broadcast"}
                </button>
              </>
            )}
            {sendStep === 3 && sendResult && (
              <div>
                <div style={{ color: sendResult.success ? ACC : "#ff7a6b", fontSize: 14, marginBottom: 10 }}>
                  {sendResult.success ? "✓ Sent" : "✕ Failed"}
                </div>
                <div style={{ ...mono, fontSize: 11, color: "#8b9490", wordBreak: "break-all" }}>
                  {sendResult.tx_hash ? `TX ${sendResult.tx_hash}` : sendResult.message}
                </div>
                <button style={{ ...primaryBtn(true), marginTop: 18 }} onClick={() => { setSendStep(0); setSendTo(""); setSendAmt(""); setSendResult(null); }}>
                  Send another
                </button>
              </div>
            )}
          </div>
        )}

        {screen === "receive" && account && (
          <div style={card}>
            <h2 style={{ marginTop: 0 }}>Receive</h2>
            <div style={{ ...mono, fontSize: 13, wordBreak: "break-all", background: "#0a0c0d", border: "1px solid rgba(255,255,255,.1)", borderRadius: 10, padding: 16, marginBottom: 14 }}>
              {account.address}
            </div>
            <button
              style={{ ...primaryBtn(true), background: copied ? "rgba(42,212,196,.15)" : ACC, color: copied ? ACC : "#000" }}
              onClick={() => { navigator.clipboard.writeText(account.address); setCopied(true); setTimeout(() => setCopied(false), 1600); }}
            >
              {copied ? "Copied to clipboard" : "Copy address"}
            </button>
          </div>
        )}

        {screen === "activity" && (
          <div>
            <h2>Activity</h2>
            {activityLoading && <div style={{ color: "#6b7570" }}>Loading real chain data…</div>}
            {!activityLoading && activity.length === 0 && <div style={{ color: "#6b7570" }}>No transactions yet.</div>}
            {activity.map((t) => <TxRow key={t.Hash} t={t} full />)}
          </div>
        )}

        {screen === "faucet" && (
          <div style={card}>
            <h2 style={{ marginTop: 0 }}>Testnet Faucet</h2>
            <p style={{ color: "#8b9490", fontSize: 12.5 }}>
              Requests real testnet AETH from the public faucet at 157.245.252.221. Rate-limited per address.
            </p>
            <button
              style={primaryBtn(faucetState === "idle")}
              disabled={faucetState !== "idle"}
              onClick={requestFaucet}
            >
              {faucetState === "sending" ? "Broadcasting…" : faucetState === "done" ? "Cooldown active" : "Request testnet AETH"}
            </button>
            {faucetMsg && <div style={{ marginTop: 12, fontSize: 11.5, color: "#8b9490", ...mono }}>{faucetMsg}</div>}
          </div>
        )}

        {(screen === "buy" || screen === "swap") && (
          <div style={card}>
            <h2 style={{ marginTop: 0 }}>{screen === "buy" ? "Buy AETH" : "Swap into AETH"}</h2>
            <div style={{ display: "flex", alignItems: "flex-start", gap: 10, background: "rgba(227,178,95,.08)", border: "1px solid rgba(227,178,95,.3)", borderRadius: 10, padding: 14 }}>
              <span style={{ color: GOLD }}>⚠</span>
              <div style={{ fontSize: 12.5, color: "#e8bf78" }}>
                Not built yet. {screen === "buy" ? "A fiat on-ramp" : "Cross-chain swaps"} would require real infrastructure
                {screen === "swap" ? " Aether doesn't have yet -- native IBC is explicitly listed as not built in the project README." : " (a payment processor integration) that doesn't exist for this project yet."}
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

function TxRow({ t, full }) {
  const aeth = uaethToAeth(parseFloat(t.Amount) || 0);
  const isSent = t.Direction === "sent";
  const failed = t.Code !== 0;
  return (
    <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", padding: "12px 0", borderBottom: "1px solid rgba(255,255,255,.06)" }}>
      <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
        <div style={{
          width: 32, height: 32, borderRadius: "50%", display: "flex", alignItems: "center", justifyContent: "center",
          background: failed ? "rgba(255,122,107,.1)" : isSent ? "rgba(255,255,255,.05)" : "rgba(42,212,196,.1)",
          border: `1px solid ${failed ? "rgba(255,122,107,.3)" : isSent ? "rgba(255,255,255,.1)" : "rgba(42,212,196,.28)"}`,
          color: failed ? "#ff7a6b" : isSent ? "#f2f4f3" : ACC, fontFamily: "'IBM Plex Mono',monospace",
        }}>
          {isSent ? "↗" : "↙"}
        </div>
        <div>
          <div style={{ fontSize: 12.5 }}>{isSent ? "Sent" : "Received"}</div>
          <div style={{ fontFamily: "'IBM Plex Mono',monospace", fontSize: 10, color: "#6b7570" }}>
            {full ? t.Hash : t.Hash.slice(0, 20) + "…"} · height {t.Height}
          </div>
        </div>
      </div>
      <div style={{ fontFamily: "'IBM Plex Mono',monospace", fontSize: 12.5, color: failed ? "#ff7a6b" : isSent ? "#f2f4f3" : ACC }}>
        {isSent ? "−" : "+"}{fmt(aeth)} AETH
      </div>
    </div>
  );
}
