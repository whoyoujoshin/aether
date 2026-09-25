import { useState } from "react";
import { Link } from "react-router-dom";

export function truncate(value: string, head = 10, tail = 6): string {
  if (value.length <= head + tail + 3) return value;
  return `${value.slice(0, head)}…${value.slice(-tail)}`;
}

/** A copy-to-clipboard button, self-contained so any component can drop one in next to a hash/address. */
export function CopyButton({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);

  async function handleCopy(e: React.MouseEvent) {
    e.preventDefault();
    e.stopPropagation();
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1200);
    } catch {
      // Clipboard API can be unavailable (non-HTTPS, permissions) --
      // fail silently rather than throw in the viewer's face over a
      // convenience feature.
    }
  }

  return (
    <button className={`copy-btn${copied ? " copied" : ""}`} onClick={handleCopy} title="Copy">
      {copied ? "✓" : "⧉"}
    </button>
  );
}

/** A truncated, monospace, copyable transaction hash linking to its detail page. */
export function TxHash({ hash, full = false }: { hash: string; full?: boolean }) {
  return (
    <span className="hash-link">
      <Link to={`/tx/${hash}`}>{full ? hash : truncate(hash)}</Link>
      <CopyButton value={hash} />
    </span>
  );
}

/** A truncated, monospace, copyable address linking to its detail page. */
export function AddressLink({ address, full = false }: { address: string; full?: boolean }) {
  if (!address) return <span className="mono">—</span>;
  return (
    <span className="hash-link">
      <Link to={`/address/${address}`}>{full ? address : truncate(address)}</Link>
      <CopyButton value={address} />
    </span>
  );
}

/** A block height linking to its detail page. */
export function BlockLink({ height }: { height: number }) {
  return (
    <Link className="mono" to={`/blocks/${height}`}>
      {height}
    </Link>
  );
}
