package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// nodeLocationsPath is the operator's file placing validators and miners
// on the Validators globe (--node-locations). Locations are what the
// operator chose to publish, at city level at most: there's no IP in the
// file or in the API, and an entry with "precision":"country" never shows
// its city, for a node run from someone's home.
var nodeLocationsPath string

// nodeLocationDTO is one node on the globe. Address is the miner account
// (aether1...) the Validators page lists it under.
type nodeLocationDTO struct {
	Address   string  `json:"address"`
	Label     string  `json:"label,omitempty"`
	City      string  `json:"city,omitempty"`
	Country   string  `json:"country"`
	Region    string  `json:"region"`
	Lat       float64 `json:"lat"`
	Lon       float64 `json:"lon"`
	Precision string  `json:"precision"` // "city" or "country"
}

type nodeLocationsResponse struct {
	Locations []nodeLocationDTO `json:"locations"`
}

// parseNodeLocations reads the file's JSON array, refusing anything that
// could publish more than meant: an IP, a city on a country-only entry,
// coordinates off the globe.
func parseNodeLocations(bz []byte) ([]nodeLocationDTO, error) {
	var raw []map[string]any
	if err := json.Unmarshal(bz, &raw); err != nil {
		return nil, err
	}
	out := make([]nodeLocationDTO, 0, len(raw))
	for i, m := range raw {
		for _, k := range []string{"ip", "host", "remote_ip"} {
			if _, ok := m[k]; ok {
				return nil, fmt.Errorf("entry %d: %q: the file takes places, not addresses; put the city and its coordinates", i, k)
			}
		}
		bz, _ := json.Marshal(m)
		var l nodeLocationDTO
		if err := json.Unmarshal(bz, &l); err != nil {
			return nil, fmt.Errorf("entry %d: %w", i, err)
		}
		if !strings.HasPrefix(l.Address, "aether1") {
			return nil, fmt.Errorf("entry %d: address: the miner account, aether1...", i)
		}
		if l.Country == "" || l.Region == "" {
			return nil, fmt.Errorf("entry %d (%s): country and region are required", i, l.Address)
		}
		if l.Lat < -90 || l.Lat > 90 || l.Lon < -180 || l.Lon > 180 {
			return nil, fmt.Errorf("entry %d (%s): lat/lon off the globe", i, l.Address)
		}
		switch l.Precision {
		case "", "city":
			l.Precision = "city"
		case "country":
			l.City = ""
		default:
			return nil, fmt.Errorf("entry %d (%s): precision is \"city\" or \"country\"", i, l.Address)
		}
		out = append(out, l)
	}
	return out, nil
}

// locations rereads the file when it changes, so an operator's edit
// shows without a restart.
var locations struct {
	sync.Mutex
	mod  time.Time
	list []nodeLocationDTO
	err  error
}

func loadNodeLocations() ([]nodeLocationDTO, error) {
	if nodeLocationsPath == "" {
		return nil, nil
	}
	locations.Lock()
	defer locations.Unlock()
	fi, err := os.Stat(nodeLocationsPath)
	if err != nil {
		return nil, err
	}
	if !fi.ModTime().Equal(locations.mod) {
		bz, err := os.ReadFile(nodeLocationsPath)
		if err == nil {
			locations.list, locations.err = parseNodeLocations(bz)
		} else {
			locations.err = err
		}
		locations.mod = fi.ModTime()
	}
	return locations.list, locations.err
}

// --- GET /api/locations ---
func handleLocations(w http.ResponseWriter, r *http.Request) {
	list, err := loadNodeLocations()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": errors.Join(errors.New("node locations"), err).Error()})
		return
	}
	if list == nil {
		list = []nodeLocationDTO{}
	}
	writeJSON(w, http.StatusOK, nodeLocationsResponse{Locations: list})
}
