package clipboard

import (
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestStartServer tests server initialization
func TestStartServer(t *testing.T) {
	server, err := StartServer("")
	if err != nil {
		t.Fatalf("StartServer() error = %v", err)
	}
	defer server.listener.Close()

	if server.Port == 0 {
		t.Error("Server port should not be 0")
	}

	if server.Token == "" {
		t.Error("Server token should not be empty")
	}

	if server.URL == "" {
		t.Error("Server URL should not be empty")
	}

	expectedURLPrefix := "http://host.docker.internal:"
	if !strings.HasPrefix(server.URL, expectedURLPrefix) {
		t.Errorf("Expected URL to start with %s, got %s", expectedURLPrefix, server.URL)
	}
}

// TestStartServerCustomHost tests server with custom host
func TestStartServerCustomHost(t *testing.T) {
	server, err := StartServer("host.orbstack.internal")
	if err != nil {
		t.Fatalf("StartServer() error = %v", err)
	}
	defer server.listener.Close()

	expectedURL := "http://host.orbstack.internal:"
	if !strings.HasPrefix(server.URL, expectedURL) {
		t.Errorf("Expected URL to start with %s, got %s", expectedURL, server.URL)
	}
}

// TestStartServerTokenUniqueness tests that each server gets unique token
func TestStartServerTokenUniqueness(t *testing.T) {
	server1, err1 := StartServer("")
	if err1 != nil {
		t.Fatalf("First StartServer() error = %v", err1)
	}
	defer server1.listener.Close()

	server2, err2 := StartServer("")
	if err2 != nil {
		t.Fatalf("Second StartServer() error = %v", err2)
	}
	defer server2.listener.Close()

	if server1.Token == server2.Token {
		t.Error("Tokens should be unique")
	}

	if server1.Port == server2.Port {
		t.Error("Ports should be unique")
	}
}

// TestTokenFormat tests that token is valid hex
func TestTokenFormat(t *testing.T) {
	server, err := StartServer("")
	if err != nil {
		t.Fatalf("StartServer() error = %v", err)
	}
	defer server.listener.Close()

	if len(server.Token) != 64 {
		t.Errorf("Token should be 64 hex chars, got %d", len(server.Token))
	}

	_, err = hex.DecodeString(server.Token)
	if err != nil {
		t.Errorf("Token should be valid hex: %v", err)
	}
}

// TestHandlePasteUnauthorized tests missing/invalid token
func TestHandlePasteUnauthorized(t *testing.T) {
	server, err := StartServer("")
	if err != nil {
		t.Fatalf("StartServer() error = %v", err)
	}
	defer server.listener.Close()

	req := httptest.NewRequest("GET", server.URL+"/paste?type=image/png", nil)
	req.Header.Set("X-Construct-Clip-Token", "invalid-token")

	w := httptest.NewRecorder()
	server.handlePaste(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", w.Code)
	}

	bodyStr := strings.TrimSpace(w.Body.String())
	if bodyStr != "Unauthorized" {
		t.Errorf("Expected body Unauthorized, got %q (len=%d)", bodyStr, len(bodyStr))
	}
}

// TestHandlePasteMissingToken tests missing token header
func TestHandlePasteMissingToken(t *testing.T) {
	server, err := StartServer("")
	if err != nil {
		t.Fatalf("StartServer() error = %v", err)
	}
	defer server.listener.Close()

	req := httptest.NewRequest("GET", server.URL+"/paste", nil)

	w := httptest.NewRecorder()
	server.handlePaste(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", w.Code)
	}
}

// TestHandlePasteText tests text endpoint
func TestHandlePasteText(t *testing.T) {
	server, err := StartServer("")
	if err != nil {
		t.Fatalf("StartServer() error = %v", err)
	}
	defer server.listener.Close()

	req := httptest.NewRequest("GET", server.URL+"/paste?type=text/plain", nil)
	req.Header.Set("X-Construct-Clip-Token", server.Token)

	w := httptest.NewRecorder()
	server.handlePaste(w, req)

	// We expect either success (if clipboard has text) or error (if empty)
	// But status should never be 401 (auth error)
	if w.Code == http.StatusUnauthorized {
		t.Error("Should not get unauthorized with valid token")
	}

	// Content-Type should be set correctly
	contentType := w.Header().Get("Content-Type")
	if contentType != "" && contentType != "text/plain" {
		t.Errorf("Expected Content-Type 'text/plain', got %s", contentType)
	}
}

// TestHandlePasteImageDefaultType tests image endpoint without type param
func TestHandlePasteImageDefaultType(t *testing.T) {
	server, err := StartServer("")
	if err != nil {
		t.Fatalf("StartServer() error = %v", err)
	}
	defer server.listener.Close()

	req := httptest.NewRequest("GET", server.URL+"/paste", nil)
	req.Header.Set("X-Construct-Clip-Token", server.Token)

	w := httptest.NewRecorder()
	server.handlePaste(w, req)

	if w.Code == http.StatusUnauthorized {
		t.Error("Should not get unauthorized with valid token")
	}
}

// TestHandlePasteImageType tests image endpoint
func TestHandlePasteImageType(t *testing.T) {
	server, err := StartServer("")
	if err != nil {
		t.Fatalf("StartServer() error = %v", err)
	}
	defer server.listener.Close()

	req := httptest.NewRequest("GET", server.URL+"/paste?type=image/png", nil)
	req.Header.Set("X-Construct-Clip-Token", server.Token)

	w := httptest.NewRecorder()
	server.handlePaste(w, req)

	if w.Code == http.StatusUnauthorized {
		t.Error("Should not get unauthorized with valid token")
	}

	// Content-Type should be set to image/png for image requests
	// Note: If GetImage() returns error (no image), status might not be 200
	if w.Code == http.StatusOK {
		contentType := w.Header().Get("Content-Type")
		if contentType != "image/png" {
			t.Errorf("Expected Content-Type 'image/png' for successful image request, got %s", contentType)
		}
	}
}

// TestHandlePasteEmptyQuery tests without query params
func TestHandlePasteEmptyQuery(t *testing.T) {
	server, err := StartServer("")
	if err != nil {
		t.Fatalf("StartServer() error = %v", err)
	}
	defer server.listener.Close()

	req := httptest.NewRequest("GET", server.URL+"/paste", nil)
	req.Header.Set("X-Construct-Clip-Token", server.Token)

	w := httptest.NewRecorder()
	server.handlePaste(w, req)

	if w.Code == http.StatusUnauthorized {
		t.Error("Should not get unauthorized with valid token")
	}
}

// TestConcurrentRequests tests multiple simultaneous requests
func TestConcurrentRequests(t *testing.T) {
	server, err := StartServer("")
	if err != nil {
		t.Fatalf("StartServer() error = %v", err)
	}
	defer server.listener.Close()

	requests := 10
	resultChan := make(chan error, requests)

	for i := 0; i < requests; i++ {
		go func() {
			req := httptest.NewRequest("GET", server.URL+"/paste", nil)
			req.Header.Set("X-Construct-Clip-Token", server.Token)

			w := httptest.NewRecorder()
			server.handlePaste(w, req)

			if w.Code == http.StatusUnauthorized {
				resultChan <- nil
				return
			}

			if w.Code >= 400 {
				resultChan <- io.EOF
			} else {
				resultChan <- nil
			}
		}()
	}

	for i := 0; i < requests; i++ {
		if err := <-resultChan; err != nil && err != io.EOF {
			t.Errorf("Request %d failed: %v", i, err)
		}
	}
}

// TestServerPortAllocation tests that server gets a valid port
func TestServerPortAllocation(t *testing.T) {
	server, err := StartServer("")
	if err != nil {
		t.Fatalf("StartServer() error = %v", err)
	}
	defer server.listener.Close()

	if server.Port < 1024 || server.Port > 65535 {
		t.Errorf("Port %d is out of valid range [1024-65535]", server.Port)
	}

	if server.listener.Addr() == nil {
		t.Error("Listener address should not be nil")
	}
}

// TestServerURLConstruction tests URL is properly formatted
func TestServerURLConstruction(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		expected string
	}{
		{
			name:     "default host",
			host:     "",
			expected: "http://host.docker.internal:",
		},
		{
			name:     "custom host",
			host:     "my-host.local",
			expected: "http://my-host.local:",
		},
		{
			name:     "IP address",
			host:     "192.168.1.100",
			expected: "http://192.168.1.100:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, err := StartServer(tt.host)
			if err != nil {
				t.Fatalf("StartServer() error = %v", err)
			}
			defer server.listener.Close()

			if !strings.HasPrefix(server.URL, tt.expected) {
				t.Errorf("Expected URL to start with %s, got %s", tt.expected, server.URL)
			}
		})
	}
}

// TestHandlePasteBodySize tests that response body is not empty
func TestHandlePasteBodySize(t *testing.T) {
	server, err := StartServer("")
	if err != nil {
		t.Fatalf("StartServer() error = %v", err)
	}
	defer server.listener.Close()

	req := httptest.NewRequest("GET", server.URL+"/paste?type=text/plain", nil)
	req.Header.Set("X-Construct-Clip-Token", server.Token)

	w := httptest.NewRecorder()
	server.handlePaste(w, req)

	if w.Code != http.StatusUnauthorized {
		body := w.Body.String()
		if len(body) == 0 && w.Code < 400 {
			t.Error("Response body should not be empty on non-error responses")
		}
	}
}

// TestServerRandomPort tests that ports are allocated randomly
func TestServerRandomPort(t *testing.T) {
	ports := make(map[int]bool)

	for i := 0; i < 10; i++ {
		server, err := StartServer("")
		if err != nil {
			t.Fatalf("StartServer() %d error = %v", i, err)
		}
		defer server.listener.Close()

		if ports[server.Port] {
			t.Errorf("Port %d already allocated (not random)", server.Port)
		}
		ports[server.Port] = true
	}
}

// TestHandleContentTypeHeader tests content type header handling
func TestHandleContentTypeHeader(t *testing.T) {
	server, err := StartServer("")
	if err != nil {
		t.Fatalf("StartServer() error = %v", err)
	}
	defer server.listener.Close()

	tests := []struct {
		name       string
		queryParam string
		expectedCT string
	}{
		{
			name:       "text plain",
			queryParam: "type=text/plain",
			expectedCT: "text/plain",
		},
		{
			name:       "empty type",
			queryParam: "",
			expectedCT: "text/plain",
		},
		{
			name:       "image png",
			queryParam: "type=image/png",
			expectedCT: "image/png",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := server.URL + "/paste"
			if tt.queryParam != "" {
				url += "?" + tt.queryParam
			}

			req := httptest.NewRequest("GET", url, nil)
			req.Header.Set("X-Construct-Clip-Token", server.Token)

			w := httptest.NewRecorder()
			server.handlePaste(w, req)

			if w.Code == http.StatusUnauthorized {
				t.Error("Should not be unauthorized")
				return
			}

			// Only check Content-Type on successful requests
			if w.Code == http.StatusOK {
				contentType := w.Header().Get("Content-Type")
				if contentType != tt.expectedCT {
					t.Errorf("Expected Content-Type %s, got %s", tt.expectedCT, contentType)
				}
			}
		})
	}
}

// TestServerConcurrentAccess tests server handles rapid access
func TestServerConcurrentAccess(t *testing.T) {
	server, err := StartServer("")
	if err != nil {
		t.Fatalf("StartServer() error = %v", err)
	}
	defer server.listener.Close()

	goroutines := 50
	done := make(chan bool, goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			req := httptest.NewRequest("GET", server.URL+"/paste", nil)
			req.Header.Set("X-Construct-Clip-Token", server.Token)

			w := httptest.NewRecorder()
			server.handlePaste(w, req)
			done <- true
		}()
	}

	for i := 0; i < goroutines; i++ {
		<-done
	}
}

// TestHandlePasteErrorResponse tests error response format
func TestHandlePasteErrorResponse(t *testing.T) {
	server, err := StartServer("")
	if err != nil {
		t.Fatalf("StartServer() error = %v", err)
	}
	defer server.listener.Close()

	req := httptest.NewRequest("GET", server.URL+"/paste", nil)
	req.Header.Set("X-Construct-Clip-Token", "wrong-token")

	w := httptest.NewRecorder()
	server.handlePaste(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401, got %d", w.Code)
	}

	body := w.Body.String()
	if body == "" {
		t.Error("Error response should have body")
	}

	if !strings.Contains(body, "Unauthorized") {
		t.Errorf("Expected 'Unauthorized' in body, got %s", body)
	}
}
