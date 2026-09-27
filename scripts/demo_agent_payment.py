#!/usr/bin/env python3
"""Drives a real agentmcp process over MCP stdio and records a transcript.

Used by scripts/demo-agent-payment.sh; not meant to be run standalone.

    demo_agent_payment.py <service-url> <pull-allowance> <out-path> -- <agentmcp-command...>
"""
import json
import subprocess
import sys
import time


def main():
    args = sys.argv[1:]
    sep = args.index("--")
    service_url, allowance, out_path = args[:sep]
    agentmcp_cmd = args[sep + 1 :]

    proc = subprocess.Popen(
        agentmcp_cmd, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, bufsize=1
    )
    next_id = 0

    def call(method, params=None, notify=False):
        nonlocal next_id
        obj = {"jsonrpc": "2.0", "method": method}
        if params is not None:
            obj["params"] = params
        my_id = None
        if not notify:
            next_id += 1
            my_id = next_id
            obj["id"] = my_id
        proc.stdin.write(json.dumps(obj) + "\n")
        proc.stdin.flush()
        if notify:
            return None
        # Skip server-initiated notifications (no "id") until our response arrives.
        while True:
            line = proc.stdout.readline()
            if not line:
                raise RuntimeError("agentmcp closed stdout; stderr:\n" + proc.stderr.read())
            msg = json.loads(line)
            if msg.get("id") == my_id:
                return msg

    def tool(name, tool_args=None):
        r = call("tools/call", {"name": name, "arguments": tool_args or {}})
        if "error" in r:
            return {"_rpc_error": r["error"]}
        if "result" not in r:
            return {"_unexpected_response": r}
        text = "".join(c.get("text", "") for c in r["result"]["content"] if c.get("type") == "text")
        try:
            return json.loads(text)
        except ValueError:
            return {"_raw": text}

    init = call(
        "initialize",
        {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "demo-driver", "version": "1"}},
    )
    call("notifications/initialized", notify=True)

    transcript = {"server": init["result"]["serverInfo"], "steps": []}

    def step(label, name, tool_args, out):
        transcript["steps"].append({"step": label, "tool": name, "args": tool_args, "result": out})
        print(f"--- {label}: {name}({json.dumps(tool_args)})", file=sys.stderr)
        print(json.dumps(out, indent=2), file=sys.stderr)

    addr = tool("get_agent_address")
    step("1. Agent checks its own address", "get_agent_address", {}, addr)

    fund = tool("request_testnet_funds")
    step("2. Agent asks the faucet to fund itself", "request_testnet_funds", {}, fund)

    bal = {}
    for _ in range(30):
        bal = tool("get_balance")
        if int(bal.get("balance", {}).get("uaeth", "0")) > 0:
            break
        time.sleep(2)
    step("3. Agent checks its balance", "get_balance", {}, bal)

    for i in range(1, 4):
        call_args = {"url": service_url, "maxAmount": "0.002 AETH", "pullAllowance": allowance, "idempotencyKey": f"demo-{i}"}
        out = tool("fetch_paid", call_args)
        step(f"4.{i} Agent calls the paid API (fetch_paid)", "fetch_paid", call_args, out)

    status = tool("get_spending_status")
    step("5. Agent's spending status", "get_spending_status", {}, status)

    purchases = tool("list_purchases")
    step("6. Agent's purchase history and receipts", "list_purchases", {}, purchases)

    with open(out_path, "w") as f:
        json.dump(transcript, f, indent=2)
    print(json.dumps({"address": addr.get("address", "")}))


if __name__ == "__main__":
    main()
