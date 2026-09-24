import { useEffect, useRef, useState } from "react";

interface FetchState<T> {
  data: T | null;
  error: string | null;
  loading: boolean;
}

/**
 * Runs fetcher() on mount and whenever deps change, tracking
 * loading/error/data state. Pass refreshMs to also poll on an
 * interval (used for the dashboard's "live" feel without a
 * websocket).
 */
export function useApi<T>(fetcher: () => Promise<T>, deps: unknown[], refreshMs?: number): FetchState<T> {
  const [state, setState] = useState<FetchState<T>>({ data: null, error: null, loading: true });
  const fetcherRef = useRef(fetcher);
  fetcherRef.current = fetcher;

  useEffect(() => {
    let cancelled = false;

    async function run(showLoading: boolean) {
      if (showLoading) setState((s) => ({ ...s, loading: true }));
      try {
        const data = await fetcherRef.current();
        if (!cancelled) setState({ data, error: null, loading: false });
      } catch (err) {
        if (!cancelled) {
          setState((s) => ({ data: s.data, error: err instanceof Error ? err.message : String(err), loading: false }));
        }
      }
    }

    run(true);

    if (refreshMs) {
      const id = setInterval(() => run(false), refreshMs);
      return () => {
        cancelled = true;
        clearInterval(id);
      };
    }
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);

  return state;
}
