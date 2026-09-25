/** s without trailing ch characters. A loop, not /ch+$/: that regex backtracks quadratically on long runs. */
export function trimEnd(s: string, ch: string): string {
  let end = s.length;
  while (end > 0 && s[end - 1] === ch) end--;
  return s.slice(0, end);
}
