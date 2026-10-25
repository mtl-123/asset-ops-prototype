//go:build windows

// Command connector is the thin local companion for central multi-user mode.
// It listens only on loopback, redeems a short-lived ticket from the central
// server, and launches the user's own mstsc. The Windows password is returned
// by the central server over TLS directly to this process; it never enters the
// browser or this process's logs.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"qiyun-asset-ops/internal/rdp"
)

type server struct {
	manager *rdp.Manager
	central string
	client  *http.Client
	origins map[string]bool

	mu     sync.Mutex
	caps   *rdp.Capability
	capsAt time.Time
}

func main() {
	central := flag.String("central", "", "central server base URL, e.g. https://ops.example.com")
	addr := flag.String("addr", "127.0.0.1:8766", "loopback listen address")
	dataDir := flag.String("data", "", "connector data directory")
	origins := flag.String("origins", "", "extra allowed browser origins, comma separated")
	flag.Parse()

	if strings.TrimSpace(*central) == "" {
		log.Fatal("connector: -central is required")
	}
	centralURL, err := url.Parse(strings.TrimSpace(*central))
	if err != nil || (centralURL.Scheme != "http" && centralURL.Scheme != "https") || centralURL.Host == "" {
		log.Fatalf("connector: invalid central URL: %v", err)
	}
	host, _, err := net.SplitHostPort(*addr)
	if err != nil || (host != "127.0.0.1" && host != "localhost" && host != "::1") {
		log.Fatalf("connector: listen address must be loopback, got %q", *addr)
	}
	if *dataDir == "" {
		base, err := os.UserConfigDir()
		if err != nil || base == "" {
			base = "."
		}
		*dataDir = filepath.Join(base, "QiyunConnector")
	}
	if err := os.MkdirAll(filepath.Join(*dataDir, "logs"), 0o700); err != nil {
		log.Fatalf("connector: create data directory: %v", err)
	}
	setupLog(filepath.Join(*dataDir, "logs", "connector.log"))
	manager, err := rdp.NewLocal(rdp.Options{DataDir: *dataDir, Platform: "windows"})
	if err != nil {
		log.Fatalf("connector: prepare RDP launcher: %v", err)
	}
	allowed := map[string]bool{originOf(centralURL): true}
	for _, item := range strings.Split(*origins, ",") {
		if item = strings.TrimSpace(item); item != "" {
			allowed[item] = true
		}
	}
	s := &server{
		manager: manager,
		central: strings.TrimRight(centralURL.String(), "/"),
		client:  &http.Client{Timeout: 30 * time.Second},
		origins: allowed,
	}
	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           s.cors(s.routes()),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	log.Printf("connector listening on http://%s central=%s", *addr, s.central)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("connector: serve: %v", err)
	}
}

func setupLog(path string) {
	if info, err := os.Stat(path); err == nil && info.Size() > 5<<20 {
		_ = os.Rename(path, path+".1")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	log.SetOutput(file)
	log.SetFlags(log.LstdFlags)
}

func originOf(u *url.URL) string {
	return u.Scheme + "://" + u.Host
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("POST /launch", s.launch)
	mux.HandleFunc("POST /signing", s.signing)
	return mux
}

func (s *server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			if !s.origins[origin] {
				writeProblem(w, http.StatusForbidden, "ORIGIN_NOT_ALLOWED", "该页面不在连接器允许的来源中")
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Qiyun-Connector")
			w.Header().Set("Access-Control-Max-Age", "600")
			if strings.EqualFold(r.Header.Get("Access-Control-Request-Private-Network"), "true") {
				w.Header().Set("Access-Control-Allow-Private-Network", "true")
			}
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method == http.MethodPost && r.Header.Get("X-Qiyun-Connector") != "1" {
			writeProblem(w, http.StatusForbidden, "CONNECTOR_HEADER_REQUIRED", "缺少连接器请求头")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) capabilities(ctx context.Context) rdp.Capability {
	s.mu.Lock()
	if s.caps != nil && time.Since(s.capsAt) < 30*time.Second {
		cached := *s.caps
		s.mu.Unlock()
		return cached
	}
	s.mu.Unlock()
	capability := s.manager.Capabilities(ctx)
	s.mu.Lock()
	s.caps, s.capsAt = &capability, time.Now()
	s.mu.Unlock()
	return capability
}

func (s *server) invalidateCapabilities() {
	s.mu.Lock()
	s.caps = nil
	s.mu.Unlock()
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "central": s.central, "version": "1",
		"capabilities": s.capabilities(r.Context()),
	})
}

func (s *server) launch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Ticket string `json:"ticket"`
	}
	body := http.MaxBytesReader(w, r.Body, 16<<10)
	if err := json.NewDecoder(body).Decode(&in); err != nil || strings.TrimSpace(in.Ticket) == "" {
		writeProblem(w, http.StatusBadRequest, "INVALID_TICKET", "缺少连接票据")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	profile, status, err := s.redeem(ctx, in.Ticket)
	if err != nil {
		writeProblem(w, status, "RDP_TICKET_REDEEM_FAILED", err.Error())
		return
	}
	result, err := s.manager.LaunchProfile(ctx, profile)
	if err != nil {
		var rdpErr *rdp.RDPError
		if errors.As(err, &rdpErr) {
			writeProblem(w, rdpErr.Status, rdpErr.Code, rdpErr.Message)
			return
		}
		writeProblem(w, http.StatusBadGateway, "RDP_LAUNCH_FAILED", "启动 mstsc 失败")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) redeem(ctx context.Context, ticket string) (rdp.Profile, int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.central+"/api/v1/rdp/tickets/redeem", nil)
	if err != nil {
		return rdp.Profile{}, http.StatusBadGateway, errors.New("无法创建票据兑换请求")
	}
	request.Header.Set("Authorization", "Bearer "+ticket)
	request.Header.Set("Accept", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return rdp.Profile{}, http.StatusBadGateway, errors.New("无法连接中心服务")
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode != http.StatusOK {
		var problem struct {
			Detail string `json:"detail"`
			Code   string `json:"code"`
		}
		_ = json.Unmarshal(payload, &problem)
		message := strings.TrimSpace(problem.Detail)
		if message == "" {
			message = "票据兑换失败"
		}
		status := response.StatusCode
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		return rdp.Profile{}, status, errors.New(message)
	}
	var profile rdp.Profile
	if err := json.Unmarshal(payload, &profile); err != nil {
		return rdp.Profile{}, http.StatusBadGateway, errors.New("中心返回的连接参数无效")
	}
	return profile, http.StatusOK, nil
}

func (s *server) signing(w http.ResponseWriter, r *http.Request) {
	status, err := s.manager.SetupSigning(r.Context())
	if err != nil {
		var rdpErr *rdp.RDPError
		if errors.As(err, &rdpErr) {
			writeProblem(w, rdpErr.Status, rdpErr.Code, rdpErr.Message)
			return
		}
		writeProblem(w, http.StatusBadGateway, "RDP_SIGNING_SETUP_FAILED", "RDP 签名初始化失败")
		return
	}
	s.invalidateCapabilities()
	writeJSON(w, http.StatusOK, status)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeProblem(w http.ResponseWriter, status int, code, detail string) {
	writeJSON(w, status, map[string]any{
		"type": "about:blank", "title": http.StatusText(status),
		"status": status, "code": code, "detail": detail,
	})
}
