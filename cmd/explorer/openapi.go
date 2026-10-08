package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/whoyoujoshin/aether/wallet"
)

// --- GET /api/openapi.json ---
//
// The explorer's API as an OpenAPI 3.1 document, so an agent (or any
// tool that reads OpenAPI) can call it without reading this source.
// Response schemas are generated from the very types the handlers
// encode, and apiRoutes is also what main registers, so the document
// can't list a route that doesn't exist or miss one that does.

type apiParam struct {
	name, description, example string
	required, integer          bool
}

type apiRoute struct {
	path        string
	handler     http.HandlerFunc
	tag         string
	summary     string
	description string
	params      []apiParam
	response    reflect.Type // the 200 body
}

func typeOf[T any]() reflect.Type { return reflect.TypeFor[T]() }

// Named bodies for handlers that answer with an ad hoc map.
type (
	tallyResponse struct {
		Tally tallyDTO  `json:"tally"`
		Votes []voteDTO `json:"votes"`
	}
	searchResponse struct {
		Kind  string `json:"kind"` // address, block or tx
		Value string `json:"value"`
	}
	servicesResponse struct {
		DirectoryAddress string       `json:"directoryAddress"`
		Services         []serviceDTO `json:"services"`
	}
)

var addrParam = apiParam{name: "addr", description: "a bech32 account address", example: "aether1...", required: true}

func apiRoutes() []apiRoute {
	return []apiRoute{
		{"/api/agents", handleAgents, "agents", "Everything an agent needs to start",
			"Chain ID, public endpoints, the faucet, the MCP wallet and its tools, payment schemes and docs, in one response.",
			nil, typeOf[agentCardDTO]()},
		{"/api/stats", handleStats, "chain", "Chain at a glance",
			"Latest height, difficulty, block reward, current epoch, treasury balance and more.",
			nil, typeOf[statsResponse]()},
		{"/api/blocks", handleBlocks, "chain", "Recent blocks, newest first", "Up to 20 blocks.", nil, typeOf[[]blockSummaryDTO]()},
		{"/api/block", handleBlock, "chain", "One block", "",
			[]apiParam{{name: "height", description: "block height", example: "120000", required: true, integer: true}}, typeOf[blockExtrasDTO]()},
		{"/api/recent-transactions", handleRecentTransactions, "chain", "Recent transactions, newest first", "",
			[]apiParam{{name: "limit", description: "how many (default 20)", example: "20", integer: true}}, typeOf[[]recentTransactionDTO]()},
		{"/api/tx", handleTx, "chain", "One transaction, decoded",
			"Result code, gas, events, transfers and decoded messages. 404 until the transaction is in a block.",
			[]apiParam{{name: "hash", description: "transaction hash, hex", example: "A1B2...", required: true}}, typeOf[txExtrasDTO]()},
		{"/api/search", handleSearch, "chain", "Classify a search term",
			"Says whether q is an address, a block height or a transaction hash.",
			[]apiParam{{name: "q", description: "address, height or hash", example: "aether1...", required: true}}, typeOf[searchResponse]()},
		{"/api/address", handleAddress, "accounts", "Balance and recent transactions of an address",
			"Balance in uaeth (1 AETH = 1,000,000 uaeth) and up to 20 recent transactions in either direction.",
			[]apiParam{addrParam}, typeOf[addressResponse]()},
		{"/api/grants", handleGrants, "accounts", "Spending permissions an address gave or received",
			"x/authz grants and x/feegrant allowances: spend limits, expiry, allowed recipients.",
			[]apiParam{addrParam}, typeOf[grantsDTO]()},
		{"/api/miner", handleMiner, "mining", "A miner's standing this epoch",
			"Registered consensus key, work this epoch, rank among eligible miners, blocks until the validator set is picked, and whether it's in the set now. For push alerts instead of polling, run cmd/minerwatch.",
			[]apiParam{addrParam}, typeOf[wallet.MinerStatus]()},
		{"/api/leaderboard", handleLeaderboard, "mining", "Work per miner in an epoch", "",
			[]apiParam{{name: "epoch", description: "epoch index (default: the current one)", example: "3000", integer: true}}, typeOf[leaderboardDTO]()},
		{"/api/validators", handleValidators, "mining", "Validators as x/pow tracks them", "", nil, typeOf[[]validatorInfoDTO]()},
		{"/api/validator-set", handleValidatorSet, "mining", "The CometBFT validator set, with miner accounts and status", "", nil, typeOf[validatorSetDTO]()},
		{"/api/locations", handleLocations, "mining", "Where validators and miners run, as their operators publish it",
			"From the explorer's --node-locations file: city (or only country) and coordinates per miner account. Never IP addresses.",
			nil, typeOf[nodeLocationsResponse]()},
		{"/api/proposals", handleProposals, "governance", "Governance proposals", "", nil, typeOf[[]proposalDTO]()},
		{"/api/proposals/tally", handleProposalTally, "governance", "A proposal's tally and votes", "",
			[]apiParam{{name: "id", description: "proposal ID", example: "1", required: true, integer: true}}, typeOf[tallyResponse]()},
		{"/api/governance/params", handleGovernanceParams, "governance", "Governance parameters", "", nil, typeOf[governanceParamsDTO]()},
		{"/api/services", handleServices, "services", "Paid APIs announced on chain",
			"The on-chain service directory: each service's price, payment schemes and recent payment activity and ratings.",
			nil, typeOf[servicesResponse]()},
		{"/api/ibc", handleIBC, "ibc", "IBC clients, connections and channels", "", nil, typeOf[ibcSummaryDTO]()},
		{"/api/helix", handleHelix, "ibc", "Both chains' recent blocks and the packets between them",
			"Aether's blocks and, when the explorer is run with --ibc-rpc, a connected IBC chain's, over the last `seconds` (at least `min` blocks each), with every IBC packet sent, received, acknowledged or timed out in them joined into one entry per packet, and the transfer channel's totals. `peers` lists every connected chain the explorer can draw; `peer` picks one.",
			[]apiParam{{name: "seconds", description: "window, 10 to 600 (default 96)", example: "96", integer: true},
				{name: "min", description: "at least this many blocks per chain, up to 50 (default 0)", example: "20", integer: true},
				{name: "peer", description: "the IBC chain to draw, by chain ID or name, from peers (default: the first)", example: "injective-888"}}, typeOf[helixDTO]()},
		{"/api/assets", handleAssets, "chain", "The tokens this explorer names",
			"AETH, and the one USDC (route and issuing denom) the explorer was told about. Any other denom is shown as is.",
			nil, typeOf[assetsResponse]()},
	}
}

var openAPIDoc = sync.OnceValues(func() ([]byte, error) {
	schemaOpts := &jsonschema.ForOptions{IgnoreInvalidTypes: true}
	paths := map[string]any{}
	for _, rt := range apiRoutes() {
		schema, err := jsonschema.ForType(rt.response, schemaOpts)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", rt.path, err)
		}
		var params []map[string]any
		for _, p := range rt.params {
			typ := "string"
			if p.integer {
				typ = "integer"
			}
			params = append(params, map[string]any{
				"name": p.name, "in": "query", "required": p.required, "description": p.description,
				"schema": map[string]any{"type": typ}, "example": p.example,
			})
		}
		op := map[string]any{
			"operationId": rt.path[len("/api/"):],
			"tags":        []string{rt.tag},
			"summary":     rt.summary,
			"responses": map[string]any{
				"200": map[string]any{"description": "OK", "content": map[string]any{"application/json": map[string]any{"schema": schema}}},
				"400": map[string]any{"$ref": "#/components/responses/Error"},
				"404": map[string]any{"$ref": "#/components/responses/Error"},
				"502": map[string]any{"$ref": "#/components/responses/Error"},
			},
		}
		if rt.description != "" {
			op["description"] = rt.description
		}
		if params != nil {
			op["parameters"] = params
		}
		paths[rt.path] = map[string]any{"get": op}
	}
	doc := map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":   "Aether explorer API",
			"version": "1",
			"description": "Read-only JSON over the Aether chain: blocks, transactions, accounts, mining, governance, " +
				"the on-chain service directory and IBC. No authentication; CORS is open. Amounts are in uaeth " +
				"(1 AETH = 1,000,000 uaeth). Start with /api/agents. Source: " + repoURL,
		},
		"externalDocs": map[string]any{"url": repoURL + "#ai-agents-start-here", "description": "AI agents: start here"},
		"paths":        paths,
		"components": map[string]any{
			"responses": map[string]any{
				"Error": map[string]any{
					"description": "The request was invalid, or the node behind the explorer failed.",
					"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{
						"type": "object", "required": []string{"error"},
						"properties": map[string]any{"error": map[string]any{"type": "string"}},
					}}},
				},
			},
		},
	}
	return json.MarshalIndent(doc, "", "  ")
})

func handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	doc, err := openAPIDoc()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Write(doc)
}
