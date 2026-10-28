package ui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/btelolabs/btelo-forge/internal/web"
)

// The services listen on these ports, or the next free one within a few.
const (
	mcpPort   = 38471
	phonePort = 38481
)

// services are the local MCP endpoint for AI tools and the page phones download exports from.
type services struct {
	mcp      *http.Server
	mcpURL   string
	mcpErr   error
	phone    *http.Server
	phoneURL []string
	phoneErr error
}

// listen opens host:port, or one of the next ports when it is taken.
func listen(host string, port int) (net.Listener, int, error) {
	var err error
	for p := port; p < port+10; p++ {
		var l net.Listener
		if l, err = net.Listen("tcp", fmt.Sprintf("%s:%d", host, p)); err == nil {
			return l, p, nil
		}
	}
	return nil, 0, err
}

func serve(handler http.Handler, l net.Listener) *http.Server {
	s := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go s.Serve(l)
	return s
}

// applyServices starts or stops each service to match the settings.
func (a *App) applyServices() {
	if a.services == nil {
		a.services = &services{}
	}
	s := a.services
	if a.settings.MCP && s.mcp == nil {
		l, port, err := listen("127.0.0.1", mcpPort)
		s.mcpErr = err
		if err == nil {
			// Each domain at its own path, so an AI set up for one is told about its tools alone.
			mux := http.NewServeMux()
			for _, domain := range mcpDomains {
				server := a.mcpServer(domain)
				mux.Handle("/mcp"+mcpDomainPaths[domain], mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
			}
			s.mcp, s.mcpURL = serve(mux, l), fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
		}
	}
	if !a.settings.MCP && s.mcp != nil {
		shutdown(s.mcp)
		s.mcp, s.mcpURL = nil, ""
	}
	if a.settings.Phone && s.phone == nil {
		if a.settings.PhoneKey == "" {
			a.settings.PhoneKey = newKey()
			a.saveSettings()
		}
		l, port, err := listen("0.0.0.0", phonePort)
		s.phoneErr = err
		if err == nil {
			key := a.settings.PhoneKey
			s.phone = serve(web.Handler(key, a.phoneSite()), l)
			s.phoneURL = nil
			for _, ip := range web.LANAddresses() {
				s.phoneURL = append(s.phoneURL, fmt.Sprintf("http://%s:%d/?k=%s", ip, port, key))
			}
		}
	}
	if !a.settings.Phone && s.phone != nil {
		shutdown(s.phone)
		s.phone, s.phoneURL = nil, nil
	}
}

func shutdown(s *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s.Shutdown(ctx)
}

func (s *services) stop() {
	for _, srv := range []*http.Server{s.mcp, s.phone} {
		if srv != nil {
			shutdown(srv)
		}
	}
}

// errBusy means the editor did not answer in time, usually because a modal system dialog is open.
var errBusy = errors.New("the editor is busy; close any open dialog in the app and try again")

// call runs fn on the UI goroutine and waits for its result.
func call[T any](a *App, fn func() (T, error)) (T, error) {
	type result struct {
		v   T
		err error
	}
	done := make(chan result, 1)
	a.post(func() {
		v, err := fn()
		done <- result{v, err}
	})
	select {
	case r := <-done:
		return r.v, r.err
	case <-time.After(30 * time.Second):
		var zero T
		return zero, errBusy
	}
}
