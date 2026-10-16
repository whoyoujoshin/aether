import { useEffect, useState, type ComponentType } from "react";
import type { PricePoint } from "@/lib/dex/types";

type ViewProps = { points: PricePoint[]; reduceMotion: boolean };

export function PriceChart({ points, label }: { points: PricePoint[]; label: string }) {
  const [View, setView] = useState<ComponentType<ViewProps> | null>(null);
  const [reduceMotion, setReduceMotion] = useState(false);

  useEffect(() => {
    const media = window.matchMedia("(prefers-reduced-motion: reduce)");
    setReduceMotion(media.matches);
    let alive = true;
    void import("./price-chart-view").then((mod) => {
      if (alive) setView(() => mod.PriceChartView);
    });
    return () => {
      alive = false;
    };
  }, []);

  return (
    <div className="h-48 w-full" role="img" aria-label={label}>
      {View ? <View points={points} reduceMotion={reduceMotion} /> : null}
    </div>
  );
}
