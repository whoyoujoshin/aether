import { useState } from "react";
import { Link, NavLink, useLocation, useNavigate } from "react-router-dom";
import { api } from "../api";
import { ChainSelector, useChain } from "../chain";

/** The shared search box logic: the backend decides whether it's an address, tx or block. */
export function useSearch() {
  const [query, setQuery] = useState("");
  const [error, setError] = useState("");
  const navigate = useNavigate();

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    const q = query.trim();
    if (!q) return;
    setError("");
    try {
      const result = await api.search(q);
      // encodeURIComponent, not a raw template splice: the search API
      // echoes back arbitrary unvalidated text as an "address" whenever
      // it doesn't match a real address/hash shape (see handleSearch's
      // fallback in cmd/explorer/main.go), and react-router's <Link>/
      // useNavigate has a known open-redirect class via backslashes in
      // an unencoded path segment (GHSA via CVE-2025-68470-style
      // bypass) -- encoding closes that regardless of the library's
      // own patch status.
      const encoded = encodeURIComponent(result.value);
      const section = result.kind === "tx" ? "tx" : result.kind === "block" ? "blocks" : "address";
      navigate(`/${section}/${encoded}`);
      setQuery("");
    } catch (err) {
      setError(err instanceof Error ? err.message : "search failed");
    }
  }

  return { query, setQuery, error, submit };
}

const links: [string, string][] = [
  ["/", "Overview"],
  ["/blocks", "Blocks"],
  ["/validators", "Validators"],
  ["/governance", "Governance"],
  ["/services", "Services"],
  ["/ibc", "IBC"],
  ["/agents", "For agents"],
];

/** home: the overview's variant, with the search in the hero instead of the bar. */
export function TopBar({ home = false }: { home?: boolean }) {
  const search = useSearch();
  // The Both / Aether / IBC switch, on the two pages that draw the helix.
  const path = useLocation().pathname;
  const withSwitch = !!useChain().ibcName && (path === "/" || path === "/blocks");

  return (
    <div className={`topbar${home ? " home" : ""}${withSwitch ? " has-switch" : ""}`}>
      <Link to="/" className="brand" aria-label="Aether Explorer home">
        <img src="/aether-mark-reversed.svg" alt="" />
        <span className="brand-word">AETHER</span>
        <span className="brand-sub">Explorer</span>
      </Link>
      <nav className="nav-links">
        {links.map(([to, label]) => (
          <NavLink key={to} to={to} end={to === "/"} className={({ isActive }) => `nav-link${isActive ? " active" : ""}`}>
            {label}
          </NavLink>
        ))}
      </nav>
      {withSwitch && <ChainSelector />}
      {!home && (
        <form className="search-form" onSubmit={search.submit} role="search">
          <input
            type="text"
            placeholder={withSwitch ? "Search Aether" : "Search address, hash or height"}
            aria-label="Search address, hash or height"
            value={search.query}
            onChange={(e) => search.setQuery(e.target.value)}
          />
          {search.error && <div className="error-banner search-error">{search.error}</div>}
        </form>
      )}
    </div>
  );
}
