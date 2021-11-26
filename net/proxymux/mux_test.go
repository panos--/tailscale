// Copyright (c) 2021 Tailscale Inc & AUTHORS All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package proxymux

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"testing"

	"tailscale.com/net/socks5"
)

func TestMux(t *testing.T) {
	s := mkScaffold(t)
	defer s.Close()

	s.checkURL(s.httpClient, false)
	s.checkURL(s.socksClient, false)
}

func TestMuxCloseSocks(t *testing.T) {
	s := mkScaffold(t)
	defer s.Close()

	s.socksListener.Close()
	s.checkURL(s.httpClient, false)
	s.checkURL(s.socksClient, true)
}

func TestMuxCloseHTTP(t *testing.T) {
	s := mkScaffold(t)
	defer s.Close()

	s.httpListener.Close()
	s.checkURL(s.httpClient, true)
	s.checkURL(s.socksClient, false)
}

func TestMuxCloseBoth(t *testing.T) {
	s := mkScaffold(t)
	defer s.Close()

	s.httpListener.Close()
	s.socksListener.Close()
	s.checkURL(s.httpClient, true)
	s.checkURL(s.socksClient, true)
}

type scaffold struct {
	t *testing.T

	// targetListener/target is the HTTP server the client wants to
	// reach. It unconditionally responds with HTTP 418 "I'm a
	// teapot".
	targetListener net.Listener
	target         http.Server
	targetURL      string

	// httpListener/httpProxy is an HTTP proxy that can proxy to
	// target.
	httpListener net.Listener
	httpProxy    http.Server

	// socksListener/socksProxy is a SOCKS5 proxy that can dial
	// targetListener.
	socksListener net.Listener
	socksProxy    *socks5.Server

	// jointListener is the mux that serves both HTTP and SOCKS5
	// proxying.
	jointListener net.Listener

	// httpClient and socksClient are HTTP clients configured to proxy
	// through httpProxy and socksProxy respectively.
	httpClient  *http.Client
	socksClient *http.Client
}

func (s *scaffold) checkURL(c *http.Client, wantErr bool) {
	s.t.Helper()
	resp, err := c.Get(s.targetURL)
	if wantErr {
		if err == nil {
			s.t.Errorf("HTTP request succeeded unexpectedly: got HTTP code %d, wanted failure", resp.StatusCode)
		}
	} else if err != nil {
		s.t.Errorf("HTTP request failed: %v", err)
	} else if c := resp.StatusCode; c != http.StatusTeapot {
		s.t.Errorf("unexpected status code: got %d, want %d", c, http.StatusTeapot)
	}
}

func (s *scaffold) Close() {
	s.jointListener.Close()
	s.socksListener.Close()
	s.httpProxy.Close()
	s.httpListener.Close()
	s.target.Close()
	s.targetListener.Close()
}

func mkScaffold(t *testing.T) (ret *scaffold) {
	t.Helper()

	ret = &scaffold{
		t: t,
	}
	var err error

	ret.targetListener, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ret.target = http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		}),
	}
	go ret.target.Serve(ret.targetListener)
	ret.targetURL = fmt.Sprintf("http://%s/", ret.targetListener.Addr().String())

	ret.jointListener, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ret.socksListener, ret.httpListener = Mux(ret.jointListener)

	httpProxy := http.Server{
		Handler: httputil.NewSingleHostReverseProxy(&url.URL{
			Scheme: "http",
			Host:   ret.targetListener.Addr().String(),
			Path:   "/",
		}),
	}
	go httpProxy.Serve(ret.httpListener)

	socksProxy := socks5.Server{}
	go socksProxy.Serve(ret.socksListener)

	ret.httpClient = &http.Client{
		Transport: &http.Transport{
			Proxy: func(*http.Request) (*url.URL, error) {
				return &url.URL{
					Scheme: "http",
					Host:   ret.jointListener.Addr().String(),
					Path:   "/",
				}, nil
			},
			DisableKeepAlives: true, // one connection per request
		},
	}

	ret.socksClient = &http.Client{
		Transport: &http.Transport{
			Proxy: func(*http.Request) (*url.URL, error) {
				return &url.URL{
					Scheme: "socks5",
					Host:   ret.jointListener.Addr().String(),
					Path:   "/",
				}, nil
			},
			DisableKeepAlives: true, // one connection per request
		},
	}

	return ret
}
