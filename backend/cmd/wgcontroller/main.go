package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type clientRequest struct {
	RoomID            int64  `json:"room_id"`
	PublicKey         string `json:"public_key"`
	PreviousPublicKey string `json:"previous_public_key"`
	PreviousVirtualIP string `json:"previous_virtual_ip"`
	VirtualIP         string `json:"virtual_ip"`
}

type peerResponse struct {
	EndpointHost string `json:"endpoint_host"`
	EndpointPort int    `json:"endpoint_port"`
}

type server struct {
	secret        string
	interfaceName string
}

func (s server) authorized(r *http.Request) bool {
	return s.secret != "" && r.Header.Get("Authorization") == "Bearer "+s.secret
}

func validKey(value string) bool {
	return len(strings.TrimSpace(value)) == 44 && strings.HasSuffix(strings.TrimSpace(value), "=")
}

func validClientIP(roomID int64, value string) bool {
	ip := net.ParseIP(strings.TrimSpace(value)).To4()
	if ip == nil {
		return false
	}
	// 07 and 08 intentionally use disjoint /24 ranges on the shared
	// interface.  Binding a lease from another room here would leak routes
	// across room lifecycles and make the controller's room_id meaningless.
	var prefix byte
	switch roomID {
	case 7:
		prefix = 7
	case 8:
		prefix = 8
	default:
		return false
	}
	return ip[0] == 10 && ip[1] == 222 && ip[2] == prefix && ip[3] >= 10 && ip[3] <= 109
}

func runWG(args ...string) ([]byte, error) {
	command := exec.Command("wg", args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("wg %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func peerAllowedIP(output, publicKey string) (string, bool) {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == publicKey {
			return fields[1], true
		}
	}
	return "", false
}

func (s server) removePeer(publicKey, expectedVirtualIP string) error {
	publicKey = strings.TrimSpace(publicKey)
	if publicKey == "" {
		return nil
	}
	if expectedVirtualIP != "" {
		output, err := runWG("show", s.interfaceName, "allowed-ips")
		if err != nil {
			return err
		}
		allowed, found := peerAllowedIP(string(output), publicKey)
		if !found {
			return nil
		}
		// Never remove a key that has been reused for a different lease.
		if allowed != expectedVirtualIP+"/32" && allowed != expectedVirtualIP {
			log.Printf("skip stale peer removal for %s: allowed-ips=%s expected=%s", publicKey, allowed, expectedVirtualIP)
			return nil
		}
	}
	if _, err := runWG("set", s.interfaceName, "peer", publicKey, "remove"); err != nil {
		return err
	}
	return nil
}

func (s server) clients(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var input clientRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil || input.RoomID < 1 || !validKey(input.PublicKey) || !validClientIP(input.RoomID, input.VirtualIP) ||
		(strings.TrimSpace(input.PreviousPublicKey) != "" && !validKey(input.PreviousPublicKey)) ||
		(strings.TrimSpace(input.PreviousVirtualIP) != "" && !validClientIP(input.RoomID, input.PreviousVirtualIP)) {
		http.Error(w, "invalid client", http.StatusBadRequest)
		return
	}
	publicKey := strings.TrimSpace(input.PublicKey)
	previousPublicKey := strings.TrimSpace(input.PreviousPublicKey)
	allowed := strings.TrimSpace(input.VirtualIP) + "/32"
	if _, err := runWG("set", s.interfaceName, "peer", publicKey, "allowed-ips", allowed); err != nil {
		log.Printf("configure client failed: %v", err)
		http.Error(w, "wireguard unavailable", http.StatusServiceUnavailable)
		return
	}
	if previousPublicKey != "" && previousPublicKey != publicKey {
		// Install the replacement first. If that fails, the old peer remains
		// available for the current session instead of being removed early.
		if err := s.removePeer(previousPublicKey, input.PreviousVirtualIP); err != nil {
			log.Printf("remove previous client peer failed (continuing): %v", err)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"state":"ready"}`))
}

func parseEndpoint(raw string) (peerResponse, error) {
	value := strings.TrimSpace(raw)
	if value == "" || value == "(none)" {
		return peerResponse{}, errors.New("endpoint unavailable")
	}
	host, portText, err := net.SplitHostPort(value)
	if err != nil {
		return peerResponse{}, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return peerResponse{}, errors.New("invalid endpoint port")
	}
	return peerResponse{EndpointHost: host, EndpointPort: port}, nil
}

func (s server) client(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	publicKey := strings.TrimPrefix(r.URL.Path, "/clients/")
	if decoded, err := netUrlPathUnescape(publicKey); err == nil {
		publicKey = decoded
	}
	if !validKey(publicKey) {
		http.Error(w, "invalid key", http.StatusBadRequest)
		return
	}
	if r.Method == http.MethodDelete {
		peers, err := runWG("show", s.interfaceName, "peers")
		if err != nil {
			http.Error(w, "wireguard unavailable", http.StatusServiceUnavailable)
			return
		}
		found := false
		for _, line := range strings.Split(string(peers), "\n") {
			if strings.TrimSpace(line) == strings.TrimSpace(publicKey) {
				found = true
				break
			}
		}
		if !found {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if _, err := runWG("set", s.interfaceName, "peer", strings.TrimSpace(publicKey), "remove"); err != nil {
			http.Error(w, "wireguard unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	output, err := runWG("show", s.interfaceName, "endpoints")
	if err != nil {
		http.Error(w, "wireguard unavailable", http.StatusServiceUnavailable)
		return
	}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == publicKey {
			endpoint, err := parseEndpoint(fields[1])
			if err != nil {
				http.Error(w, "endpoint unavailable", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(endpoint)
			return
		}
	}
	http.Error(w, "peer not found", http.StatusNotFound)
}

func netUrlPathUnescape(value string) (string, error) {
	// WireGuard base64 keys only require decoding %2B, %2F and %3D here.
	replacer := strings.NewReplacer("%2B", "+", "%2b", "+", "%2F", "/", "%2f", "/", "%3D", "=", "%3d", "=")
	return replacer.Replace(value), nil
}

func main() {
	listen := os.Getenv("WEL_WG_CONTROLLER_LISTEN")
	if listen == "" {
		listen = "127.0.0.1:51821"
	}
	s := server{secret: os.Getenv("WEL_WG_CONTROLLER_SECRET"), interfaceName: os.Getenv("WEL_WG_INTERFACE")}
	if s.interfaceName == "" {
		s.interfaceName = "welwg0"
	}
	if s.secret == "" {
		log.Fatal("WEL_WG_CONTROLLER_SECRET is required")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/clients", s.clients)
	mux.HandleFunc("/clients/", s.client)
	httpServer := &http.Server{Addr: listen, Handler: mux, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second}
	log.Printf("WireGuard controller listening on %s for %s", listen, s.interfaceName)
	log.Fatal(httpServer.ListenAndServe())
}
