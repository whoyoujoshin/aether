import { useState } from "react";
import { INSTRUCTION_HINT, runInstruction } from "@/lib/dex/agent";
import { baseToInput, formatBase, formatPercentFromBps, parseBase } from "@/lib/dex/format";
import { setAgentLimit, setSlippage, useDex } from "@/lib/dex/store";
import { panel, primaryBtn } from "./chrome";

const PRESETS = [
  "pool",
  "quote 1000 aeth to usdc",
  "swap 250 aeth to usdc",
  "swap 1000 usdc to aeth",
  "swap 5000 aeth to usdc",
] as const;

const SLIPPAGE = [10, 50, 100] as const;

export function AgentDesk() {
  const dex = useDex();
  const [text, setText] = useState("swap 250 aeth to usdc");
  const [picked, setPicked] = useState<string | null>(null);
  const turn = dex.calls.find((call) => call.id === picked) ?? dex.calls[0];

  function run(instruction: string) {
    const done = runInstruction(instruction);
    setPicked(done.id);
  }

  return (
    <form
      className={panel}
      onSubmit={(event) => {
        event.preventDefault();
        if (text.trim()) run(text);
      }}
    >
      <div className="flex items-baseline justify-between gap-3">
        <h2 className="text-base font-medium">Agent</h2>
        <p className="font-mono text-xs text-muted">dex-agent</p>
      </div>
      <p className="mt-2 text-sm text-muted">
        Per trade {formatBase(BigInt(dex.agentLimitAeth))} AETH · {formatBase(BigInt(dex.agentLimitUsdc))} USDC.
        High impact settles only if the instruction ends in confirm.
      </p>

      <label htmlFor="agent-instruction" className="mt-4 block text-xs text-muted">
        Instruction
      </label>
      <input
        id="agent-instruction"
        value={text}
        autoComplete="off"
        autoCorrect="off"
        spellCheck={false}
        onChange={(event) => setText(event.target.value)}
        className="mt-2 h-12 w-full rounded-sm border border-line bg-surface-2 px-3 font-mono text-base text-fg outline-none"
      />
      <button type="submit" className={`${primaryBtn} mt-3`}>
        Run
      </button>
      <div className="mt-3 flex flex-wrap gap-2">
        {PRESETS.map((preset) => (
          <button
            key={preset}
            type="button"
            className="h-11 rounded-sm border border-line px-3 text-sm text-muted hover:text-fg"
            onClick={() => {
              setText(preset);
              run(preset);
            }}
          >
            {preset}
          </button>
        ))}
      </div>
      <p className="mt-3 text-xs text-muted">{INSTRUCTION_HINT}</p>

      <div className="mt-4">
        <p className="text-xs text-muted">Slippage</p>
        <div className="mt-2 grid grid-cols-3 gap-2" role="group" aria-label="Slippage">
          {SLIPPAGE.map((bps) => {
            const selected = dex.slippageBps === bps;
            return (
              <button
                key={bps}
                type="button"
                aria-pressed={selected}
                onClick={() => setSlippage(bps)}
                className={
                  selected
                    ? "h-11 rounded-sm border border-fg text-sm text-fg"
                    : "h-11 rounded-sm border border-line text-sm text-muted hover:text-fg"
                }
              >
                {formatPercentFromBps(BigInt(bps))}
              </button>
            );
          })}
        </div>
      </div>

      <OwnerLimits aeth={dex.agentLimitAeth} usdc={dex.agentLimitUsdc} />

      {turn ? (
        <div className="mt-4 grid gap-3">
          <CallBlock title="Call" body={JSON.stringify(turn.call, null, 2)} />
          <CallBlock title="Result" body={JSON.stringify(turn.result, null, 2)} />
        </div>
      ) : (
        <p className="mt-4 text-sm text-muted">No call yet. Run an instruction. The request and the result land here.</p>
      )}

      {dex.calls.length > 0 ? (
        <ol className="mt-4 border-t border-line">
          {dex.calls.map((call) => (
            <li key={call.id} className="border-b border-line">
              <button
                type="button"
                onClick={() => setPicked(call.id)}
                className="flex h-11 w-full items-center justify-between gap-3 text-left"
              >
                <span className="truncate font-mono text-sm text-fg">{call.instruction}</span>
                <span className={call.result.ok === true ? "text-xs text-muted" : "text-xs text-danger"}>
                  {call.result.ok === true ? "ok" : String(call.result.error ?? "error")}
                </span>
              </button>
            </li>
          ))}
        </ol>
      ) : null}
    </form>
  );
}

/** The owner sets the per-trade limit here. It is not an agent tool, so the agent can't raise its own cap. */
function OwnerLimits({ aeth, usdc }: { aeth: string; usdc: string }) {
  const [aethText, setAethText] = useState(baseToInput(BigInt(aeth)));
  const [usdcText, setUsdcText] = useState(baseToInput(BigInt(usdc)));
  const aethLimit = parseBase(aethText);
  const usdcLimit = parseBase(usdcText);
  const valid = aethLimit !== null && aethLimit > 0n && usdcLimit !== null && usdcLimit > 0n;
  return (
    <fieldset className="mt-4 border-t border-line pt-4">
      <legend className="text-xs text-muted">Owner: per-trade limit (not an agent tool)</legend>
      <div className="mt-2 grid grid-cols-2 gap-2">
        <label className="text-xs text-muted">
          AETH
          <input
            value={aethText}
            inputMode="decimal"
            onChange={(event) => setAethText(event.target.value)}
            className="mt-1 h-11 w-full rounded-sm border border-line bg-surface-2 px-3 font-mono text-sm text-fg outline-none"
          />
        </label>
        <label className="text-xs text-muted">
          USDC
          <input
            value={usdcText}
            inputMode="decimal"
            onChange={(event) => setUsdcText(event.target.value)}
            className="mt-1 h-11 w-full rounded-sm border border-line bg-surface-2 px-3 font-mono text-sm text-fg outline-none"
          />
        </label>
      </div>
      <button
        type="button"
        disabled={!valid}
        onClick={() => {
          if (!valid) return;
          setAgentLimit("aeth", aethLimit);
          setAgentLimit("usdc", usdcLimit);
        }}
        className="mt-2 h-11 w-full rounded-sm border border-line text-sm text-muted hover:text-fg disabled:opacity-50"
      >
        Set limit
      </button>
    </fieldset>
  );
}

function CallBlock({ title, body }: { title: string; body: string }) {
  return (
    <div>
      <p className="text-xs text-muted">{title}</p>
      <pre className="mt-1 max-h-64 overflow-auto rounded-sm bg-surface-2 p-3 font-mono text-xs leading-relaxed break-all text-fg">
        {body}
      </pre>
    </div>
  );
}
