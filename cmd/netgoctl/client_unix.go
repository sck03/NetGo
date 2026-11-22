//go:build !windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/term"
	"io"
	"net"
	"net/http"
	"netgo/internal/ipc"
	"netgo/internal/site"
	"os"
	"path/filepath"
	"time"
)

func clientCommand(action, socket, input, id, alias string, replace bool) error {
	if socket == "" {
		root, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		socket = filepath.Join(root, "NetGo", "client.sock")
	}
	method, path := "GET", "/desktop/"+action
	var body any
	switch action {
	case "status", "profiles":
	case "import":
		if input == "" {
			return errors.New("--input site profile required")
		}
		f, err := os.Open(input)
		if err != nil {
			return err
		}
		defer f.Close()
		var p site.Profile
		if err = site.Decode(f, &p); err != nil {
			return err
		}
		if err = p.Validate(); err != nil {
			return err
		}
		body = p
		method = "POST"
	case "connect":
		if !site.ValidID(id) || alias == "" {
			return errors.New("--site-id and --alias required")
		}
		fmt.Fprint(os.Stderr, "账号密码：")
		password, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return err
		}
		defer clear(password)
		body = struct {
			Site, Alias, Password string
			Replace               bool
		}{id, alias, string(password), replace}
		method = "POST"
	case "disconnect":
		method = "POST"
	default:
		return errors.New("client commands: import, connect, disconnect, status, profiles")
	}
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
		defer clear(data)
		body = nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return ipc.Dial(ctx, socket) }}
	defer transport.CloseIdleConnections()
	cl := &http.Client{Transport: transport, Timeout: 40 * time.Second}
	request, err := http.NewRequestWithContext(ctx, method, "http://client"+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := cl.Do(request)
	if err != nil {
		return errors.New("Linux client IPC unavailable; start netgo-client-service as the current user")
	}
	defer response.Body.Close()
	var result any
	if err = json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&result); err != nil {
		return err
	}
	if response.StatusCode != 200 {
		return fmt.Errorf("client operation rejected: %v", result)
	}
	return output(result)
}
