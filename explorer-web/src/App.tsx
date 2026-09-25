import { Routes, Route } from "react-router-dom";
import { TopBar } from "./components/TopBar";
import Dashboard from "./pages/Dashboard";
import Validators from "./pages/Validators";
import Governance from "./pages/Governance";
import Address from "./pages/Address";
import Transaction from "./pages/Transaction";
import Blocks from "./pages/Blocks";
import Block from "./pages/Block";
import Services from "./pages/Services";

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
        <Route path="/address/:address" element={<Address />} />
        <Route path="/tx/:hash" element={<Transaction />} />
      </Routes>
      <div className="footer">Aether Explorer</div>
    </div>
  );
}
