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
      navigate(result.kind === "tx" ? `/tx/${result.value}` : `/address/${result.value}`);
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
        <NavLink to="/validators" className={({ isActive }) => `nav-link${isActive ? " active" : ""}`}>
          Validators
        </NavLink>
        <NavLink to="/governance" className={({ isActive }) => `nav-link${isActive ? " active" : ""}`}>
          Governance
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
