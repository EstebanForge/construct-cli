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

// StartServer starts the clipboard server on a random port
func StartServer() (*Server, error) {
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

	// Determine URL host (host.docker.internal is standard, but we return the port)
	// The client inside container will use host.docker.internal
	url := fmt.Sprintf("http://host.docker.internal:%d", port)

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
		fmt.Fprintf(os.Stderr, "[Clipboard Server] serve error: %v\n", err)
	}
}

func (s *Server) handlePaste(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(os.Stderr, "[Clipboard Server] Received paste request from %s\n", r.RemoteAddr)

	// Verify token
	if r.Header.Get("X-Construct-Clip-Token") != s.Token {
		fmt.Fprintf(os.Stderr, "[Clipboard Server] Unauthorized request (invalid token)\n")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	contentType := r.URL.Query().Get("type")

	if contentType == "text/plain" || contentType == "" {
		data, err := GetText()
		if err != nil {
			fmt.Fprintf(os.Stderr, "[Clipboard Server] GetText error: %v\n", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(os.Stderr, "[Clipboard Server] Serving %d bytes of text data\n", len(data))
		w.Header().Set("Content-Type", "text/plain")
		if _, err := w.Write(data); err != nil {
			fmt.Fprintf(os.Stderr, "[Clipboard Server] write text error: %v\n", err)
		}
		return
	}

	// Get image from host clipboard
	data, err := GetImage()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[Clipboard Server] GetImage error: %v\n", err)
		if err == ErrNoImage {
			http.Error(w, "No image in clipboard", http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	fmt.Fprintf(os.Stderr, "[Clipboard Server] Serving %d bytes of image data\n", len(data))
	w.Header().Set("Content-Type", "image/png")
	if _, err := w.Write(data); err != nil {
		fmt.Fprintf(os.Stderr, "[Clipboard Server] write image error: %v\n", err)
	}
}
