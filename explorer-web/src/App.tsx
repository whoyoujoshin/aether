import { Routes, Route } from "react-router-dom";
import { TopBar } from "./components/TopBar";
import Dashboard from "./pages/Dashboard";
import Validators from "./pages/Validators";
import Governance from "./pages/Governance";
import Address from "./pages/Address";
import Transaction from "./pages/Transaction";

export default function App() {
  return (
    <div className="layout">
      <TopBar />
      <Routes>
        <Route path="/" element={<Dashboard />} />
        <Route path="/validators" element={<Validators />} />
        <Route path="/governance" element={<Governance />} />
        <Route path="/address/:address" element={<Address />} />
        <Route path="/tx/:hash" element={<Transaction />} />
      </Routes>
      <div className="footer">Aether Explorer</div>
    </div>
  );
}
