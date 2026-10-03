# Node locations on the explorer's Validators globe

The Validators page's globe and its "voting power by region" bar show
where validators and miners run, **as their operators choose to publish
it**. Nothing is looked up from IP addresses: the explorer reads a small
JSON file on the server and serves it at `/api/locations`.

## The file

One entry per miner account (the `aether1...` address the Validators page
lists the validator or miner under):

```json
[
  {"address": "aether13cfj5...", "label": "seed",   "city": "New York",      "country": "US", "region": "N. America", "lat": 40.71, "lon": -74.01},
  {"address": "aether1jkh9g...", "label": "sync3",  "city": "San Francisco", "country": "US", "region": "N. America", "lat": 37.77, "lon": -122.42},
  {"address": "aether1ara7r...", "label": "sync4",  "city": "Amsterdam",     "country": "NL", "region": "Europe",     "lat": 52.37, "lon": 4.90},
  {"address": "aether1k05ym...", "label": "peer-1", "country": "US", "region": "N. America", "lat": 39.8, "lon": -98.6, "precision": "country"}
]
```

- `address`, `country`, `region`, `lat`, `lon` are required. `label` is the
  name shown when a row is hovered.
- `precision: "country"` publishes only the country: the city is dropped
  even if present, and the pin goes wherever `lat`/`lon` say, so use the
  country's middle. peer-1 runs on a home PC, so its entry is
  country-only, with the middle of the US as its point.
- The loader refuses an entry with an `ip`, `host` or `remote_ip` field,
  coordinates off the globe, or an address that isn't `aether1...`.
- Use the city of the data centre, not anything finer. Coordinates to two
  decimals (about 1 km) are plenty.
- The sync3 and sync4 cities above are where their new servers are
  (San Francisco, Amsterdam). Check the seed's against its droplet's
  region, and fill in the full miner addresses from
  `aetherd query pow active-validators`.

## Running it

On the seed, save the file as `/root/node-locations.json` and add to the
`aether-explorer` service's `ExecStart`:

```
--node-locations /root/node-locations.json --faucet-api http://127.0.0.1:8080
```

then `systemctl daemon-reload && systemctl restart aether-explorer`. Edits
to the file show within a minute without a restart.
`curl -s localhost:8081/api/locations | jq` shows what's published.

`--faucet-api` connects the new Faucet page. The faucet needs restarting
on the new binary too (its drip log and agent registry live next to its
keyring by default), and it must trust the explorer's address: its
default `--trusted-proxies` (localhost) already does when both run on the
seed.
