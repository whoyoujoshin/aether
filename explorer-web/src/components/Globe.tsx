import { useEffect, useMemo, useRef, useState } from "react";
import { geoDistance, geoGraticule10, geoOrthographic, geoPath } from "d3-geo";
import { feature } from "topojson-client";
import type { Topology } from "topojson-specification";
import land110 from "world-atlas/land-110m.json";

// Natural Earth land at 1:110m (world-atlas), drawn once per frame with an
// orthographic projection: the 4a "Node locations" globe.

export interface GlobePin {
  id: string;
  lat: number;
  lon: number;
  weight: number; // relative size, 0..1
  hollow?: boolean; // jailed / banned
}

const landTopo = land110 as unknown as Topology;
const land = feature(landTopo, landTopo.objects.land);
const graticule = geoGraticule10();

export function Globe({
  pins,
  color,
  focus,
  size = 240,
}: {
  pins: GlobePin[];
  color: string;
  focus: string | null; // pin id to turn to and enlarge
  size?: number;
}) {
  const [rot, setRot] = useState<[number, number]>([20, -25]);
  const drag = useRef<{ x: number; y: number; rot: [number, number] } | null>(null);
  const target = useRef<[number, number] | null>(null);

  // Spin slowly; turn toward the focused pin instead while one is set.
  useEffect(() => {
    let raf = 0;
    let last = performance.now();
    function frame(now: number) {
      const dt = Math.min(64, now - last);
      last = now;
      if (!drag.current) {
        setRot(([l, p]) => {
          const t = target.current;
          if (t) {
            let dl = ((t[0] - l + 540) % 360) - 180;
            const dp = t[1] - p;
            const k = Math.min(1, dt / 160);
            return [l + dl * k, p + dp * k];
          }
          return [(l + dt * 0.006) % 360, p + (-25 - p) * Math.min(1, dt / 2000)];
        });
      }
      raf = requestAnimationFrame(frame);
    }
    raf = requestAnimationFrame(frame);
    return () => cancelAnimationFrame(raf);
  }, []);

  const focused = pins.find((p) => p.id === focus);
  target.current = focused ? [-focused.lon, -focused.lat] : null;

  const r = size / 2 - 4;
  const projection = useMemo(() => geoOrthographic().scale(r).translate([size / 2, size / 2]).clipAngle(90), [r, size]);
  projection.rotate([rot[0], rot[1]]);
  const path = geoPath(projection);
  const center: [number, number] = [-rot[0], -rot[1]];

  return (
    <svg
      width={size}
      height={size}
      viewBox={`0 0 ${size} ${size}`}
      className="globe"
      onPointerDown={(e) => {
        (e.target as Element).setPointerCapture?.(e.pointerId);
        drag.current = { x: e.clientX, y: e.clientY, rot };
      }}
      onPointerMove={(e) => {
        const d = drag.current;
        if (!d) return;
        setRot([d.rot[0] + (e.clientX - d.x) * 0.4, Math.max(-80, Math.min(80, d.rot[1] - (e.clientY - d.y) * 0.4))]);
      }}
      onPointerUp={() => (drag.current = null)}
      onPointerCancel={() => (drag.current = null)}
      role="img"
      aria-label="Globe of node locations"
    >
      <defs>
        <radialGradient id="globe-shade" cx="40%" cy="35%" r="70%">
          <stop offset="0%" stopColor="#1f1b17" />
          <stop offset="100%" stopColor="#0e0c0b" />
        </radialGradient>
      </defs>
      <circle cx={size / 2} cy={size / 2} r={r} fill="url(#globe-shade)" stroke="#3a342e" />
      <path d={path(graticule) ?? ""} fill="none" stroke="#2a2521" strokeWidth={0.6} />
      <path d={path(land) ?? ""} fill="#26221e" stroke="#3a342e" strokeWidth={0.5} />
      {pins.map((p) => {
        if (geoDistance([p.lon, p.lat], center) > Math.PI / 2 - 0.02) return null;
        const xy = projection([p.lon, p.lat]);
        if (!xy) return null;
        const on = p.id === focus;
        const pr = (3 + 5 * Math.sqrt(Math.max(0, Math.min(1, p.weight)))) * (on ? 1.6 : 1);
        return (
          <g key={p.id}>
            {on && <circle cx={xy[0]} cy={xy[1]} r={pr + 6} fill="none" stroke={color} strokeOpacity={0.5} />}
            <circle
              cx={xy[0]}
              cy={xy[1]}
              r={pr}
              fill={p.hollow ? "none" : color}
              stroke={color}
              strokeWidth={p.hollow ? 1.5 : 0}
              style={{ filter: `drop-shadow(0 0 ${on ? 8 : 4}px ${color})` }}
            />
          </g>
        );
      })}
    </svg>
  );
}
