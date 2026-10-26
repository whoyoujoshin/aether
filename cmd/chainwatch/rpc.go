package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// nodeView is what CometBFT /status says about one node.
type nodeView struct {
	Height     int64
	CatchingUp bool
	Network    string
	Moniker    string
	Validator  string
	LatestTime time.Time
}

// checker reads CometBFT RPC. Tests substitute a fake.
type checker interface {
	Status(ctx context.Context, rpcURL string) (nodeView, error)
	Validators(ctx context.Context, rpcURL string) ([]valPower, int64, error)
}

type httpChecker struct {
	client *http.Client
}

func newHTTPChecker() *httpChecker {
	return &httpChecker{client: &http.Client{Timeout: 15 * time.Second}}
}

func (h *httpChecker) Status(ctx context.Context, rpcURL string) (nodeView, error) {
	var body statusBody
	if err := h.get(ctx, rpcURL+"/status", &body); err != nil {
		return nodeView{}, err
	}
	if body.Error != nil && body.Error.Message != "" {
		return nodeView{}, fmt.Errorf("status: %s", body.Error.Message)
	}
	height, err := strconv.ParseInt(body.Result.SyncInfo.LatestBlockHeight, 10, 64)
	if err != nil || height <= 0 {
		return nodeView{}, fmt.Errorf("status: latest_block_height %q", body.Result.SyncInfo.LatestBlockHeight)
	}
	var when time.Time
	if t := body.Result.SyncInfo.LatestBlockTime; t != "" {
		when, _ = time.Parse(time.RFC3339Nano, t)
	}
	return nodeView{
		Height:     height,
		CatchingUp: body.Result.SyncInfo.CatchingUp,
		Network:    body.Result.NodeInfo.Network,
		Moniker:    body.Result.NodeInfo.Moniker,
		Validator:  strings.ToUpper(body.Result.ValidatorInfo.Address),
		LatestTime: when,
	}, nil
}

func (h *httpChecker) Validators(ctx context.Context, rpcURL string) ([]valPower, int64, error) {
	var all []valPower
	var height int64
	for page := 1; page <= 20; page++ {
		u := fmt.Sprintf("%s/validators?per_page=100&page=%d", rpcURL, page)
		var body validatorsBody
		if err := h.get(ctx, u, &body); err != nil {
			return nil, 0, err
		}
		if body.Error != nil && body.Error.Message != "" {
			return nil, 0, fmt.Errorf("validators: %s", body.Error.Message)
		}
		if height == 0 {
			height, _ = strconv.ParseInt(body.Result.BlockHeight, 10, 64)
		}
		for _, v := range body.Result.Validators {
			p, err := strconv.ParseInt(v.VotingPower, 10, 64)
			if err != nil {
				return nil, 0, fmt.Errorf("validators: voting_power %q", v.VotingPower)
			}
			all = append(all, valPower{Address: v.Address, Power: p})
		}
		total, _ := strconv.ParseInt(body.Result.Total, 10, 64)
		if len(body.Result.Validators) == 0 || (total > 0 && int64(len(all)) >= total) {
			return all, height, nil
		}
	}
	return nil, 0, fmt.Errorf("validators: pagination did not finish")
}

func (h *httpChecker) get(ctx context.Context, rawURL string, dest any) error {
	if _, err := url.Parse(rawURL); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "aether-chainwatch")
	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	bz, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, rawURL)
	}
	if err := json.Unmarshal(bz, dest); err != nil {
		return fmt.Errorf("decode %s: %w", rawURL, err)
	}
	return nil
}

type statusBody struct {
	Result struct {
		NodeInfo struct {
			Network string `json:"network"`
			Moniker string `json:"moniker"`
		} `json:"node_info"`
		SyncInfo struct {
			LatestBlockHeight string `json:"latest_block_height"`
			LatestBlockTime   string `json:"latest_block_time"`
			CatchingUp        bool   `json:"catching_up"`
		} `json:"sync_info"`
		ValidatorInfo struct {
			Address string `json:"address"`
		} `json:"validator_info"`
	} `json:"result"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type validatorsBody struct {
	Result struct {
		BlockHeight string `json:"block_height"`
		Validators  []struct {
			Address     string `json:"address"`
			VotingPower string `json:"voting_power"`
		} `json:"validators"`
		Count string `json:"count"`
		Total string `json:"total"`
	} `json:"result"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}
