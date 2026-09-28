package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/whoyoujoshin/aether/wallet"
)

// nodeError says in plain words when the node itself couldn't be
// reached, instead of passing on gRPC's transport text.
func nodeError(err error) error {
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded:
		return fmt.Errorf("can't reach the Aether node at %s -- check this computer's internet connection; if that's fine, the node may be down (%v)", grpcEndpoint, err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("the Aether node at %s didn't answer in time (%v)", grpcEndpoint, err)
	}
	return err
}

// GET /api/version -> this build and what it's connected to, for the
// desktop app's Settings.
func handleVersion(w http.ResponseWriter, r *http.Request) {
	build := "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			build = info.Main.Version
		}
		var rev, modified string
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				modified = s.Value
			}
		}
		if build == "unknown" && len(rev) >= 7 {
			build = rev[:7]
			if modified == "true" {
				build += "+dirty"
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"build":      build,
		"grpc":       grpcEndpoint,
		"chainId":    chainID,
		"keyringDir": keyringDir,
	})
}

// GET /api/chain -> the chain at a glance (see wallet.ChainStatus).
func handleChain(w http.ResponseWriter, r *http.Request) {
	client, err := wallet.NewClient(grpcEndpoint)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	st, err := client.ChainStatus(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, nodeError(err))
		return
	}
	writeJSON(w, http.StatusOK, st)
}
