import { useState } from "react";
import { NavLink, useNavigate } from "react-router-dom";
import { api } from "../api";

export function TopBar() {
  const [query, setQuery] = useState("");
  const [error, setError] = useState("");
  const navigate = useNavigate();

  async function handleSearch(e: React.FormEvent) {
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

  return (
    <div className="topbar">
      <div className="brand">
        <span className="brand-mark">⚡</span> Aether Explorer
      </div>
      <nav className="nav-links">
        <NavLink to="/" end className={({ isActive }) => `nav-link${isActive ? " active" : ""}`}>
          Overview
        </NavLink>
        <NavLink to="/blocks" className={({ isActive }) => `nav-link${isActive ? " active" : ""}`}>
          Blocks
        </NavLink>
        <NavLink to="/validators" className={({ isActive }) => `nav-link${isActive ? " active" : ""}`}>
          Validators
        </NavLink>
        <NavLink to="/governance" className={({ isActive }) => `nav-link${isActive ? " active" : ""}`}>
          Governance
        </NavLink>
        <NavLink to="/services" className={({ isActive }) => `nav-link${isActive ? " active" : ""}`}>
          Services
        </NavLink>
        <NavLink to="/agents" className={({ isActive }) => `nav-link${isActive ? " active" : ""}`}>
          For agents
        </NavLink>
      </nav>
      <form className="search-form" onSubmit={handleSearch}>
        <input
          className="search-input"
          type="text"
          placeholder="Search by address or transaction hash"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
        <button className="search-button" type="submit">
          Search
        </button>
      </form>
      {error && <div className="error-banner" style={{ position: "absolute", top: 56, right: 24 }}>{error}</div>}
    </div>
  );
}
