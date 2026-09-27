import { useState } from "react";
import { Link } from "react-router-dom";
import { int, shortAddr, shortHash } from "../format";

export { truncate } from "../format";

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
    <button
      type="button"
      className={`copy-btn${copied ? " copied" : ""}`}
      onClick={handleCopy}
      title={copied ? "Copied" : "Copy"}
      aria-label="Copy"
    />
  );
}

/** A truncated, monospace, copyable transaction hash linking to its detail page. */
export function TxHash({ hash, full = false, copy = true }: { hash: string; full?: boolean; copy?: boolean }) {
  return (
    <span className="hash-link">
      <Link to={`/tx/${hash}`} title={hash}>
        {full ? hash.toUpperCase() : shortHash(hash)}
      </Link>
      {copy && <CopyButton value={hash} />}
    </span>
  );
}

/**
 * A truncated, monospace, copyable address linking to its detail page.
 * plain: bone-colored, for counterparties in dense lists.
 */
export function AddressLink({
  address,
  full = false,
  copy = true,
  plain = false,
}: {
  address: string;
  full?: boolean;
  copy?: boolean;
  plain?: boolean;
}) {
  if (!address) return <span className="mono faint">—</span>;
  return (
    <span className={`hash-link${plain ? " plain" : ""}`}>
      <Link to={`/address/${encodeURIComponent(address)}`} title={address}>
        {full ? address : shortAddr(address)}
      </Link>
      {copy && <CopyButton value={address} />}
    </span>
  );
}

/** A block height linking to its detail page. */
export function BlockLink({ height }: { height: number }) {
  return (
    <Link className="mono" to={`/blocks/${height}`}>
      {int(height)}
    </Link>
  );
}
