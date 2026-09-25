import { test } from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { AetherClient, DIRECTORY_ADDRESS, Key, findServices, ratingMemo, type IncomingPayment } from "../src/index.js";

test("findServices: reputation counts only paying raters, and splits out trusted ones", async () => {
  const payee = Key.random().address;
  const [me, friend, stranger, sock] = [Key.random(), Key.random(), Key.random(), Key.random()].map((k) => k.address);
  const srv = createServer((req, res) => {
    res.end(JSON.stringify({ x402Version: 1, name: "Weather", description: "", network: "aether-testnet-1", payTo: payee, price: "20000", priceAeth: "0.02", schemes: ["aether-memo"] }));
  });
  await new Promise<void>((r) => srv.listen(0, "127.0.0.1", r));
  const url = `http://127.0.0.1:${(srv.address() as { port: number }).port}`;
  const p = (height: number, from: string, memo: string, amount = 1n, code = 0): IncomingPayment => ({ hash: `H${height}`, height, code, from, amountUaeth: amount, memo });
  const byAddress: Record<string, IncomingPayment[]> = {
    [DIRECTORY_ADDRESS]: [
      p(10, payee, "x402-service:" + url),
      p(60, me, ratingMemo(url, 5)),
      p(61, friend, ratingMemo(url + "/", 3)),
      p(62, stranger, ratingMemo(url, 1)),
      p(63, sock, ratingMemo(url, 1)), // never paid
      p(64, stranger, ratingMemo(url, 2)), // replaces stranger's 1
      p(65, friend, "x402-rate:9:" + url), // malformed
    ],
    [payee]: [p(50, me, "inv", 20_000n), p(51, friend, "inv", 20_000n), p(52, stranger, "inv", 20_000n), p(53, payee, "self", 99n), p(54, sock, "inv", 20_000n, 5)],
  };
  const client = {
    chainId: "aether-testnet-1",
    latestHeight: async () => 100,
    incomingPayments: async (address: string, since = 1) => (byAddress[address] ?? []).filter((x) => x.height >= since),
  } as unknown as AetherClient;
  try {
    const [s] = await findServices(client, { allowPrivate: true, trusted: [me, friend] });
    const r = s.reputation!;
    assert.equal(r.payers, 3);
    assert.equal(r.payments, 3);
    assert.equal(r.volumeUaeth, 60_000n);
    assert.deepEqual(r.ratings, { count: 3, average: 10 / 3 });
    assert.deepEqual(r.trustedRatings, { count: 2, average: 4 });
    assert.equal(r.raters.find((x) => x.rater === stranger)?.score, 2, "latest rating wins");
    const [noRep] = await findServices(client, { allowPrivate: true, reputation: false });
    assert.equal(noRep.reputation, undefined);
  } finally {
    srv.close();
  }
  assert.throws(() => ratingMemo(url, 0));
  assert.equal(ratingMemo("https://W.example/", 4), "x402-rate:4:https://w.example");
});
