import { useEffect, useState } from "react";
import { Routes, Route, Link, useLocation } from "react-router-dom";
import { api } from "./api";
import { setAssets } from "./format";
import { TopBar } from "./components/TopBar";
import Validators from "./pages/Validators";
import Governance from "./pages/Governance";
import Address from "./pages/Address";
import Transaction from "./pages/Transaction";
import Block from "./pages/Block";
import Services from "./pages/Services";
import Agents from "./pages/Agents";
import Faucet from "./pages/Faucet";
import Quest from "./pages/Quest";
import IBC from "./pages/IBC";
import Accounts from "./pages/Accounts";
import HelixOverview from "./pages/HelixOverview";
import HelixBlocks from "./pages/HelixBlocks";
import { ChainProvider, useChain } from "./chain";

function NotFound() {
  return (
    <div className="page">
      <h1 className="page-title">Page not found</h1>
      <div className="page-sub">
        Nothing lives at this address. <Link to="/">Back to the overview</Link>
      </div>
    </div>
  );
}

/** Helix overview once chain context is ready (Aether live; IBC ghost without --ibc-rpc). */
function Overview() {
  const { ready } = useChain();
  if (!ready) return <TopBar />;
  return <HelixOverview />;
}

function BlocksPage() {
  const { ready } = useChain();
  if (!ready) return null;
  return <HelixBlocks />;
}

export default function App() {
  return (
    <ChainProvider>
      <Shell />
    </ChainProvider>
  );
}

function Shell() {
  // The overview draws its own top bar inside its hero glow.
  const home = useLocation().pathname === "/";
  // Which denoms have names (USDC over the explorer's one channel); redraw once known.
  const [, setAssetsLoaded] = useState(false);
  useEffect(() => {
    api.assets().then((r) => { setAssets(r.assets); setAssetsLoaded(true); }).catch(() => {});
  }, []);

  return (
    <div className="layout">
      {!home && <TopBar />}
      <Routes>
        <Route path="/" element={<Overview />} />
        <Route path="/blocks" element={<BlocksPage />} />
        <Route path="/blocks/:height" element={<Block />} />
        <Route path="/validators" element={<Validators />} />
        <Route path="/governance" element={<Governance />} />
        <Route path="/services" element={<Services />} />
        <Route path="/ibc" element={<IBC />} />
        <Route path="/accounts" element={<Accounts />} />
        <Route path="/accounts/:address" element={<Accounts />} />
        <Route path="/agents" element={<Agents />} />
        <Route path="/faucet" element={<Faucet />} />
        <Route path="/quest" element={<Quest />} />
        <Route path="/address/:address" element={<Address />} />
        <Route path="/tx/:hash" element={<Transaction />} />
        <Route path="*" element={<NotFound />} />
      </Routes>
      <div className="footer">
        <img src="/aether-mark-reversed.svg" alt="" />
        Aether Explorer · read live from the chain
        <nav className="footer-links" aria-label="Community">
          <a href="https://discord.gg/fQmTTcevfq" target="_blank" rel="noopener noreferrer">
            Discord
          </a>
          <a href="https://x.com/BuildAether" target="_blank" rel="noopener noreferrer">
            X
          </a>
          <a href="https://github.com/whoyoujoshin/aether" target="_blank" rel="noopener noreferrer">
            GitHub
          </a>
        </nav>
      </div>
    </div>
  );
}
