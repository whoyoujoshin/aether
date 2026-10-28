import { useEffect, useState } from "react";
import { api, MinerStatus, NodeLocation, ValidatorSetEntry } from "../api";
import { Globe, GlobePin } from "../components/Globe";
import { useApi } from "../hooks";
import { AddressLink } from "../components/Hash";
import { ErrorBanner, HeadStat, Legend, StackBar, Tabs, pct } from "../components/ui";
import { int, truncate, utc } from "../format";


function formatShare(share: string | undefined): string {
  if (!share) return "—";
  const n = Number(share);
  if (Number.isNaN(n)) return "—";
  return `${(n * 100).toFixed(n < 0.001 ? 3 : 1)}%`;
}

function formatSeatWork(n: string | undefined): string {
  if (!n || n === "0") return "0";
  try {
    const big = BigInt(n);
    if (big >= 1_000_000_000n) return `${(Number(big) / 1e9).toFixed(2)}B`;
    if (big >= 1_000_000n) return `${(Number(big) / 1e6).toFixed(2)}M`;
    if (big >= 1_000n) return `${(Number(big) / 1e3).toFixed(1)}k`;
    return n;
  } catch {
    return n;
  }
}

function formatTenure(ratio: string): string {
  const n = Number(ratio);
  if (!ratio || Number.isNaN(n)) return "—";
  return `${(n * 100).toFixed(n < 0.001 ? 3 : 1)}%`;
}

const statusColor: Record<ValidatorSetEntry["status"], string> = {
  active: "var(--green)",
  missing: "var(--warn)",
  pending: "var(--muted)",
  banned: "var(--danger)",
};

const SIGNER_COLS = "40px minmax(0,1.3fr) minmax(0,1fr) minmax(0,1fr) 90px 150px 90px";
const MINER_COLS = "40px minmax(0,1fr) 220px 90px";
const shareColors = ["#c0503a", "#d8d1c5", "#9a9186", "#6b645b", "#3a342e"];

const TABS = ["Signers", "PoW miners"] as const;

function place(l: NodeLocation | undefined): string {
  if (!l) return "";
  return l.city ? `${l.city}, ${l.country}` : l.country;
}

export default function Validators() {
  const set = useApi(api.validatorSet, [], 10000);

  // Seat work for each validator: the 3-epoch window the seat rule
  // measures the 1% bar against. One request per account; the set is
  // small (TopKSize is 21).
  const [seat, setSeat] = useState<Map<string, MinerStatus>>(new Map());
  useEffect(() => {
    const accounts = (set.data?.validators ?? []).map((v) => v.account).filter(Boolean);
    if (accounts.length === 0) return;
    let cancelled = false;
    Promise.all(
      accounts.map((a) => api.miner(a).then((st) => [a, st] as const).catch(() => null))
    ).then((rows) => {
      if (cancelled) return;
      const m = new Map<string, MinerStatus>();
      for (const r of rows) if (r) m.set(r[0], r[1]);
      setSeat(m);
    });
    return () => { cancelled = true; };
  }, [set.data]);
  const leaderboard = useApi(() => api.leaderboard(), [], 10000);
  const located = useApi(api.locations, [], 60000);
  const [tab, setTab] = useState<(typeof TABS)[number]>("Signers");
  const [hover, setHover] = useState<string | null>(null);
  const where = new Map((located.data?.locations ?? []).map((l) => [l.address, l]));

  const vs = set.data;
  const signing = (vs?.validators ?? []).filter((v) => v.signed.length > 0);
  const maxPower = Math.max(1, ...signing.map((v) => v.votingPower));
  const signedCount = signing.reduce((a, v) => a + v.signed.filter(Boolean).length, 0);
  const slots = signing.reduce((a, v) => a + v.signed.length, 0);

  // ?? []: an explorer API from before the leaderboard DTO leaves
  // "entries" out entirely when nobody has mined this epoch yet.
  const entries = leaderboard.data?.entries ?? [];
  const totalWork = entries.reduce((a, e) => a + e.work, 0);
  const maxWork = Math.max(1, ...entries.map((e) => e.work));
  const share = [
    ...entries.slice(0, 4).map((e, i) => ({ label: truncate(e.address, 10, 5), value: e.work, color: shareColors[i] })),
    ...(entries.length > 4 ? [{ label: "others", value: entries.slice(4).reduce((a, e) => a + e.work, 0), color: shareColors[4] }] : []),
  ];
  const leader = entries[0];
  const leaderShare = leader && totalWork ? leader.work / totalWork : 0;

  // Voting power by region, from the operators' published locations.
  const byRegion = new Map<string, number>();
  const countries = new Set<string>();
  for (const v of signing) {
    const l = where.get(v.account);
    if (l) countries.add(l.country);
    const region = l?.region ?? "Unlisted";
    byRegion.set(region, (byRegion.get(region) ?? 0) + v.votingPower);
  }
  const regionTotal = signing.reduce((a, v) => a + v.votingPower, 0);
  const regions = [...byRegion.entries()]
    .sort((a, b) => (a[0] === "Unlisted" ? 1 : b[0] === "Unlisted" ? -1 : b[1] - a[1]))
    .map(([label, value], i) => ({ label, value, color: label === "Unlisted" ? "#3a342e" : shareColors[Math.min(i, shareColors.length - 2)] }));
  const largest = regions.length ? Math.max(...regions.filter((r) => r.label !== "Unlisted").map((r) => r.value), 0) : 0;

  const pins: GlobePin[] =
    tab === "Signers"
      ? signing.flatMap((v) => {
          const l = where.get(v.account);
          return l ? [{ id: v.account, lat: l.lat, lon: l.lon, weight: v.votingPower / maxPower, hollow: v.status === "banned" }] : [];
        })
      : entries.flatMap((e) => {
          const l = where.get(e.address);
          return l ? [{ id: e.address, lat: l.lat, lon: l.lon, weight: e.work / maxWork }] : [];
        });
  const hovered = hover ? where.get(hover) : undefined;
  const rowProps = (id: string) => ({
    onMouseEnter: () => setHover(id),
    onMouseLeave: () => setHover((h) => (h === id ? null : h)),
    style: hover === id ? { background: "rgba(192,80,58,.07)" } : undefined,
  });

  return (
    <div className="page">
      <div className="validators-head">
        <div>
          <h1 className="page-title">Validators</h1>
          <div className="page-sub">The nodes that sign Aether blocks, and the miners whose proof-of-work earns them a seat.</div>
          <div className="head-stats" style={{ marginTop: 28, justifyContent: "flex-start" }}>
            <HeadStat label="Active set">
              {vs ? int(signing.length) : "—"}
              {vs && vs.topKSize > 0 && <span className="faint" style={{ fontSize: 15 }}> / {vs.topKSize}</span>}
            </HeadStat>
            <HeadStat label={`Miners · epoch ${leaderboard.data ? int(leaderboard.data.epoch) : "—"}`}>{leaderboard.data ? int(entries.length) : "—"}</HeadStat>
            <HeadStat label={`Signatures · last ${vs?.window ?? 25} blocks`}>
              <span className="green">{slots ? pct(signedCount, slots, 2) : "—"}</span>
            </HeadStat>
          </div>
          {where.size > 0 && regionTotal > 0 && (
            <div className="card card-pad" style={{ marginTop: 22 }}>
              <div style={{ display: "flex", justifyContent: "space-between", marginBottom: 14, gap: 12 }}>
                <span className="eyebrow">Voting power by region</span>
                <span className="mono faint" style={{ fontSize: 11 }}>
                  {countries.size} {countries.size === 1 ? "country" : "countries"} · largest {pct(largest, regionTotal, 0)}
                </span>
              </div>
              <StackBar segments={regions} height={10} />
              <div className="region-legend">
                {regions.map((r) => (
                  <span key={r.label}>
                    <span className="swatch" style={{ background: r.color }} />
                    {r.label} <span className="mono faint">{pct(r.value, regionTotal, 0)}</span>
                  </span>
                ))}
              </div>
            </div>
          )}
        </div>
        <div className="card globe-card">
          <div className="eyebrow">Node locations</div>
          <div className="globe-wrap">
            <Globe pins={pins} color={tab === "Signers" ? "#c0503a" : "#e4d9c6"} focus={hovered ? hover : null} />
          </div>
          <div className="globe-foot">
            <span>
              {hovered ? (
                <span className="text-2">
                  {hovered.label || truncate(hover!, 10, 5)} · {place(hovered)}
                </span>
              ) : where.size > 0 ? (
                "Drag to spin · hover a row to locate"
              ) : (
                "No locations published yet"
              )}
            </span>
            <span>approx. · as operators publish</span>
          </div>
        </div>
      </div>

      <Tabs tabs={TABS} value={tab} onChange={setTab} />

      {tab === "Signers" && (
        <div className="card">
          <ErrorBanner error={set.error} />
          {set.loading && !vs ? (
            <div className="loading">Loading…</div>
          ) : vs && vs.validators.length > 0 ? (
            <div className="scroll-x">
              <div className="rows">
                <div className="row th" style={{ gridTemplateColumns: SIGNER_COLS }}>
                  <span>#</span>
                  <span>Validator</span>
                  <span>Voting power</span>
                  <span title="Seat work over the 3-epoch window, the share the 1% bar is measured against">
                    Seat work
                  </span>
                  <span className="right" title="Time spent bonded out of the time it's been eligible">
                    Tenure
                  </span>
                  <span>Last {vs.window} blocks</span>
                  <span className="right">Status</span>
                </div>
                {vs.validators.map((v, i) => (
                  <div
                    key={v.consensusAddress || v.account}
                    className="row hover"
                    onMouseEnter={rowProps(v.account).onMouseEnter}
                    onMouseLeave={rowProps(v.account).onMouseLeave}
                    style={{ gridTemplateColumns: SIGNER_COLS, ...rowProps(v.account).style }}
                  >
                    <span className="mono faint">{i + 1}</span>
                    <div style={{ display: "flex", alignItems: "center", gap: 12, minWidth: 0 }}>
                      <div className="initials">{v.account ? v.account.slice(7, 9) : "??"}</div>
                      <div style={{ minWidth: 0 }}>
                        <div style={{ fontWeight: 500, display: "flex", gap: 8, alignItems: "center" }}>
                          {v.account ? <AddressLink address={v.account} copy={false} /> : <span className="muted">unknown account</span>}
                          {v.bootstrap && (
                            <span className="tag" style={{ fontSize: 11, color: "var(--muted)", background: "var(--cell)" }} title="A genesis validator, tracked by x/pow like any other">
                              genesis
                            </span>
                          )}
                        </div>
                        <div className="mono muted ellipsis" style={{ fontSize: 12, marginTop: 2 }} title={v.enteredAtUnix ? `in the set since ${utc(v.enteredAtUnix)}` : undefined}>
                          {v.consensusAddress ? truncate(v.consensusAddress, 8, 6) : "not in the signing set yet"}
                          {where.has(v.account) && ` · ${place(where.get(v.account))}`}
                        </div>
                      </div>
                    </div>
                    <div>
                      <div className="mono" style={{ display: "flex", justifyContent: "space-between", fontSize: 13, fontWeight: 500, gap: 8 }}>
                        <span className="ellipsis">{int(v.votingPower)}</span>
                        <span className="muted">{pct(v.votingPower, vs.totalPower)}</span>
                      </div>
                      <div className="power-bar">
                        <div style={{ width: `${(v.votingPower / maxPower) * 100}%` }} />
                      </div>
                    </div>
                    <div title={seat.get(v.account)?.seatWork ? `window ${seat.get(v.account)!.seatWork} of ${seat.get(v.account)!.seatWorkTotal ?? "—"}` : "seat work not reported"}>
                      <div className="mono" style={{ display: "flex", justifyContent: "space-between", fontSize: 13, fontWeight: 500, gap: 8 }}>
                        <span className="ellipsis">{formatSeatWork(seat.get(v.account)?.seatWork)}</span>
                        <span className="muted">{formatShare(seat.get(v.account)?.workShare)}</span>
                      </div>
                      <div className="power-bar">
                        <div style={{ width: `${Math.min(100, (Number(seat.get(v.account)?.workShare ?? 0) || 0) * 100)}%` }} />
                      </div>
                    </div>
                    <span className="right mono" style={{ fontSize: 13 }}>
                      {formatTenure(v.tenureRatio)}
                    </span>
                    {v.signed.length > 0 ? (
                      <div className="uptime" style={{ gridTemplateColumns: `repeat(${v.signed.length},1fr)` }} title={`${v.signed.length - v.missed} of ${v.signed.length} signed`}>
                        {v.signed.map((s, k) => (
                          <div key={k} className={s ? "" : "miss"} />
                        ))}
                      </div>
                    ) : (
                      <span className="faint" style={{ fontSize: 12 }}>
                        joins at the next epoch
                      </span>
                    )}
                    <span className="right status-text" style={{ color: statusColor[v.status] }}>
                      {v.status.toUpperCase()}
                    </span>
                  </div>
                ))}
              </div>
            </div>
          ) : (
            <div className="empty">No validators.</div>
          )}
        </div>
      )}

      {tab === "PoW miners" && (
        <div className="split w340">
          <div className="card">
            <ErrorBanner error={leaderboard.error} />
            {leaderboard.loading && !leaderboard.data ? (
              <div className="loading">Loading…</div>
            ) : entries.length > 0 ? (
              <div className="scroll-x">
                <div className="rows" style={{ minWidth: 560 }}>
                  <div className="row th" style={{ gridTemplateColumns: MINER_COLS }}>
                    <span>#</span>
                    <span>Miner</span>
                    <span>Work · epoch {int(leaderboard.data!.epoch)}</span>
                    <span className="right">Share</span>
                  </div>
                  {entries.map((e, i) => (
                    <div
                      key={e.address}
                      className="row hover"
                      onMouseEnter={rowProps(e.address).onMouseEnter}
                      onMouseLeave={rowProps(e.address).onMouseLeave}
                      style={{ gridTemplateColumns: MINER_COLS, ...rowProps(e.address).style }}
                    >
                      <span className="mono faint">{i + 1}</span>
                      <span style={{ fontSize: 13, minWidth: 0 }}>
                        <AddressLink address={e.address} />
                        {where.has(e.address) && <span className="muted" style={{ fontSize: 12 }}> · {place(where.get(e.address))}</span>}
                      </span>
                      <div className="mono" style={{ display: "flex", alignItems: "center", gap: 10, fontSize: 13, fontWeight: 500 }}>
                        {int(e.work)}
                        <div className="power-bar" style={{ flex: 1, marginTop: 0 }}>
                          <div style={{ width: `${(e.work / maxWork) * 100}%` }} />
                        </div>
                      </div>
                      <span className="right mono" style={{ fontSize: 13 }}>
                        {pct(e.work, totalWork)}
                      </span>
                    </div>
                  ))}
                </div>
              </div>
            ) : (
              <div className="empty">No work submitted this epoch yet.</div>
            )}
          </div>
          <div className="card-glow">
            <div className="eyebrow red">Work share · epoch {leaderboard.data ? int(leaderboard.data.epoch) : "—"}</div>
            <div style={{ margin: "18px 0" }}>
              <StackBar segments={share} height={12} />
            </div>
            <Legend segments={share} />
            <div className="muted" style={{ fontSize: 12, marginTop: 14, lineHeight: 1.5 }}>
              {entries.length === 0
                ? "Work counts native MsgSubmitPoW submissions accepted this epoch."
                : leaderShare > 1 / 3
                  ? `One miner has more than a third of this epoch's work (${pct(leader!.work, totalWork)}).`
                  : "No single miner above 33% this epoch."}{" "}
              Each epoch, the top {vs?.topKSize || "K"} miners by native work are selected as validators.
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
