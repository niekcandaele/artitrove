package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"
)

// healthURL derives the /healthz URL from a listen address. An empty or unspecified host
// becomes 127.0.0.1. Named ports are rejected: a shell-less image may lack /etc/services.
func healthURL(listen string) (string, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", err
	}
	if _, err := strconv.Atoi(port); err != nil {
		return "", fmt.Errorf("listen port %q is not numeric", port)
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/healthz", nil
}

// healthcheck returns nil if GET /healthz on the listen address answers 200.
func healthcheck(ctx context.Context, listen string) error {
	url, err := healthURL(listen)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz returned %s", resp.Status)
	}
	return nil
}
