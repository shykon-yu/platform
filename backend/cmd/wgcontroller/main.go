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

func validIP(value string) bool { return net.ParseIP(strings.TrimSpace(value)) != nil }

func runWG(args ...string) ([]byte, error) {
	command := exec.Command("wg", args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("wg %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return output, nil
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
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil || input.RoomID < 1 || !validKey(input.PublicKey) || !validIP(input.VirtualIP) ||
		(strings.TrimSpace(input.PreviousPublicKey) != "" && !validKey(input.PreviousPublicKey)) {
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
		if _, err := runWG("set", s.interfaceName, "peer", previousPublicKey, "remove"); err != nil {
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
