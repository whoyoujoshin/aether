import { Area, AreaChart, ResponsiveContainer, Tooltip, YAxis } from "recharts";
import type { PricePoint } from "@/lib/dex/types";

function Tip({ active, payload }: { active?: boolean; payload?: Array<{ value?: number }> }) {
  const value = payload?.[0]?.value;
  if (!active || typeof value !== "number") return null;
  return (
    <div className="rounded-xs border border-line bg-bg px-2 py-1 font-mono text-xs text-fg tabular-nums">
      {value.toFixed(4)}
    </div>
  );
}

export function PriceChartView({
  points,
  reduceMotion,
}: {
  points: PricePoint[];
  reduceMotion: boolean;
}) {
  const values = points.map((point) => point.price);
  const min = Math.min(...values);
  const max = Math.max(...values);
  const pad = Math.max((max - min) * 0.25, 0.002);

  return (
    <ResponsiveContainer width="100%" height="100%">
      <AreaChart data={points} margin={{ top: 8, right: 4, bottom: 0, left: 0 }}>
        <defs>
          <linearGradient id="aetherMid" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="var(--color-fg)" stopOpacity={0.16} />
            <stop offset="100%" stopColor="var(--color-fg)" stopOpacity={0} />
          </linearGradient>
        </defs>
        <YAxis
          domain={[min - pad, max + pad]}
          width={52}
          tickCount={3}
          axisLine={false}
          tickLine={false}
          tickFormatter={(value: number) => value.toFixed(3)}
          tick={{
            fill: "var(--color-muted)",
            fontSize: 12,
            fontFamily: "JetBrains Mono, ui-monospace, monospace",
          }}
        />
        <Tooltip content={<Tip />} cursor={{ stroke: "var(--color-line)" }} />
        <Area
          type="monotone"
          dataKey="price"
          stroke="var(--color-fg)"
          strokeWidth={1.5}
          fill="url(#aetherMid)"
          isAnimationActive={!reduceMotion}
          dot={false}
          activeDot={{ r: 3, fill: "var(--color-fg)", stroke: "none" }}
        />
      </AreaChart>
    </ResponsiveContainer>
  );
}
