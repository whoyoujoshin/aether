"""clients/testdata/fees.json: the fee rule every client applies (the Go wallet's
TestFeeFor_SharedVectors checks the same file)."""

import json
import pathlib

import pytest

from aether_client import fee_for, parse_gas_prices

FILE = json.loads((pathlib.Path(__file__).parents[2] / "testdata" / "fees.json").read_text())


@pytest.mark.parametrize("v", FILE["vectors"], ids=[v["name"] for v in FILE["vectors"]])
def test_shared_vectors(v):
    f = fee_for(parse_gas_prices(v["prices"]), v["denom"], v["gas"], v["tip"])
    assert (f"{f[0]}{f[1]}" if f else "") == v["fee"]


def test_tip_under_one_is_refused():
    with pytest.raises(ValueError, match="at least 1"):
        fee_for(parse_gas_prices("0.0001uaeth"), "uaeth", 1, 0.5)
