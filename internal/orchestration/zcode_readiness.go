package orchestration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// ZCodeLocalReadiness reports only the configured local server and model
// availability. It is not an authorization to run a model or agent.
type ZCodeLocalReadiness struct {
	Configured      bool   `json:"configured"`
	Reachable       bool   `json:"reachable"`
	ModelFound      bool   `json:"modelFound"`
	AvailableModels int    `json:"availableModels"`
	Status          string `json:"status"`
}

func (s *Store) ProbeZCodeLocalModel() (ZCodeLocalReadiness, error) {
	model, err := s.GetZCodeLocalModel()
	if err != nil {
		return ZCodeLocalReadiness{}, err
	}
	state := ZCodeLocalReadiness{Configured: model.Configured, Status: "unconfigured"}
	if !model.Configured {
		return state, nil
	}

	// No proxies, remote DNS, cross-host redirects, arbitrary paths or
	// user-supplied HTTP headers can take this probe outside localhost.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	target := strings.TrimSuffix(model.BaseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return state, err
	}
	req.Header.Set("Accept", "application/json")
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if host != "127.0.0.1" && host != "::1" {
				return nil, errors.New("non-loopback dial rejected")
			}
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
		DisableKeepAlives:     true,
		ResponseHeaderTimeout: 2 * time.Second,
	}
	defer transport.CloseIdleConnections()
	client := http.Client{Transport: transport, Timeout: 3 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		state.Status = "unreachable"
		return state, nil
	}
	defer response.Body.Close()
	state.Reachable = true
	if response.StatusCode != http.StatusOK {
		state.Status = "unexpected_response"
		return state, nil
	}
	const max = 64 * 1024
	body, err := io.ReadAll(io.LimitReader(response.Body, max+1))
	if err != nil || len(body) > max {
		state.Status = "unexpected_response"
		return state, nil
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err = decoder.Decode(&payload); err != nil || payload.Data == nil {
		state.Status = "unexpected_response"
		return state, nil
	}
	var trailing any
	if err = decoder.Decode(&trailing); err != io.EOF {
		state.Status = "unexpected_response"
		return state, nil
	}
	state.AvailableModels = len(payload.Data)
	for _, item := range payload.Data {
		if item.ID == model.ModelID {
			state.ModelFound = true
			break
		}
	}
	if state.ModelFound {
		state.Status = "ready"
	} else {
		state.Status = "model_missing"
	}
	return state, nil
}
