package clipboard

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
)

// Server represents the clipboard server
type Server struct {
	Port     int
	Token    string
	URL      string
	listener net.Listener
}

// StartServer starts the clipboard server on a random port.
func StartServer(host string) (*Server, error) {
	// Generate random token

	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, fmt.Errorf("failed to generate token: %w", err)
	}
	token := hex.EncodeToString(tokenBytes)

	// Listen on random port (all interfaces to allow container access)
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		return nil, fmt.Errorf("failed to start listener: %w", err)
	}

	port := listener.Addr().(*net.TCPAddr).Port

	// Determine URL host (default to host.docker.internal).
	if host == "" {
		host = "host.docker.internal"
	}
	url := fmt.Sprintf("http://%s:%d", host, port)

	server := &Server{
		Port:     port,
		Token:    token,
		URL:      url,
		listener: listener,
	}

	// Start serving in background
	go server.serve()

	return server, nil
}

func (s *Server) serve() {
	mux := http.NewServeMux()
	mux.HandleFunc("/paste", s.handlePaste)

	// We use the existing listener
	if err := http.Serve(s.listener, mux); err != nil {
		logf("[Clipboard Server] serve error: %v\n", err)
	}
}

func (s *Server) handlePaste(w http.ResponseWriter, r *http.Request) {
	logf("[Clipboard Server] Received paste request from %s\n", r.RemoteAddr)

	// Verify token
	if r.Header.Get("X-Construct-Clip-Token") != s.Token {
		logf("[Clipboard Server] Unauthorized request (invalid token)\n")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	contentType := r.URL.Query().Get("type")

	if contentType == "text/plain" || contentType == "" {
		data, err := GetText()
		if err != nil {
			logf("[Clipboard Server] GetText error: %v\n", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		logf("[Clipboard Server] Serving %d bytes of text data\n", len(data))
		w.Header().Set("Content-Type", "text/plain")
		if _, err := w.Write(data); err != nil {
			logf("[Clipboard Server] write text error: %v\n", err)
		}
		return
	}

	// Get image from host clipboard
	data, err := GetImage()
	if err != nil {
		logf("[Clipboard Server] GetImage error: %v\n", err)
		if err == ErrNoImage {
			http.Error(w, "No image in clipboard", http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	logf("[Clipboard Server] Serving %d bytes of image data\n", len(data))
	w.Header().Set("Content-Type", "image/png")
	if _, err := w.Write(data); err != nil {
		logf("[Clipboard Server] write image error: %v\n", err)
	}
}

func logf(format string, args ...any) {
	if os.Getenv("CONSTRUCT_CLIPBOARD_LOG") == "1" {
		fmt.Fprintf(os.Stderr, format, args...)
	}
}
