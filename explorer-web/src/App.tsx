import { Routes, Route, Link } from "react-router-dom";
import { TopBar } from "./components/TopBar";
import Dashboard from "./pages/Dashboard";
import Validators from "./pages/Validators";
import Governance from "./pages/Governance";
import Address from "./pages/Address";
import Transaction from "./pages/Transaction";
import Blocks from "./pages/Blocks";
import Block from "./pages/Block";
import Services from "./pages/Services";
import Agents from "./pages/Agents";

function NotFound() {
  return (
    <div className="page">
      <h1 className="page-title">Page not found</h1>
      <div className="empty-state">
        Nothing lives at this address. <Link to="/">Back to the overview</Link>
      </div>
    </div>
  );
}

export default function App() {
  return (
    <div className="layout">
      <TopBar />
      <Routes>
        <Route path="/" element={<Dashboard />} />
        <Route path="/blocks" element={<Blocks />} />
        <Route path="/blocks/:height" element={<Block />} />
        <Route path="/validators" element={<Validators />} />
        <Route path="/governance" element={<Governance />} />
        <Route path="/services" element={<Services />} />
        <Route path="/agents" element={<Agents />} />
        <Route path="/address/:address" element={<Address />} />
        <Route path="/tx/:hash" element={<Transaction />} />
        <Route path="*" element={<NotFound />} />
      </Routes>
      <div className="footer">Aether Explorer</div>
    </div>
  );
}
