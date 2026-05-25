package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"nudgebee/code-analysis-agent/common"
	"nudgebee/code-analysis-agent/config"
)

type Server struct {
	config         *config.Config
	agenticHandler *AgenticAnalyzeHandler
	httpServer     *http.Server
	logger         *common.Logger
}

func NewServer(cfg *config.Config, agenticHandler *AgenticAnalyzeHandler) *Server {
	logger := common.NewLogger("server", "http-server", "system", map[string]any{
		"port": cfg.Server.Port,
	})
	return &Server{
		config:         cfg,
		agenticHandler: agenticHandler,
		logger:         logger,
	}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()

	// Health check endpoint
	mux.HandleFunc("/health", s.healthHandler)

	// Info endpoint
	mux.HandleFunc("/info", s.infoHandler)

	// Analysis endpoint
	mux.HandleFunc("/analyze", s.analyzeHandler)

	// Root endpoint
	mux.HandleFunc("/", s.rootHandler)

	s.httpServer = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.config.Server.Port),
		Handler:      mux,
		ReadTimeout:  s.config.Server.ReadTimeout,
		WriteTimeout: s.config.Server.WriteTimeout,
	}

	// Start server in a goroutine
	go func() {
		s.logger.Log(common.EventAnalysisStart, "Server starting", map[string]any{
			"port":      s.config.Server.Port,
			"endpoints": []string{"/health", "/info", "/analyze"},
		})
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error(common.EventAnalysisFailure, "Server error", err, map[string]any{
				"port": s.config.Server.Port,
			})
		}
	}()

	// Wait for interrupt signal to gracefully shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	s.logger.Log(common.EventAnalysisComplete, "Shutting down server", map[string]any{
		"shutdown_timeout": s.config.Server.ShutdownTimeout.String(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), s.config.Server.ShutdownTimeout)
	defer cancel()

	return s.httpServer.Shutdown(ctx)
}

func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"status":    "healthy",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"service":   "code-analysis-agent",
	}); err != nil {
		s.logger.Error(common.EventAnalysisFailure, "Failed to write health check response", err, nil)
	}
}

func (s *Server) infoHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"service":     "code-analysis-agent",
		"version":     "1.0.0",
		"description": "Intelligent code analysis agent that correlates application logs with source code",
		"endpoints": map[string]string{
			"/health":  "Health check",
			"/info":    "Service information",
			"/analyze": "Perform code analysis",
		},
	}); err != nil {
		s.logger.Error(common.EventAnalysisFailure, "Failed to write info response", err, nil)
	}
}

func (s *Server) analyzeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request
	var req AgenticAnalyzeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	// Process request
	ctx := r.Context()
	response, err := s.agenticHandler.HandleAgenticAnalyze(ctx, req)
	if err != nil {
		http.Error(w, fmt.Sprintf("Analysis failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Return response
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error(common.EventAnalysisFailure, "Failed to write analysis response", err, nil)
	}
}

func (s *Server) rootHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	_, err := fmt.Fprint(w, `
<!DOCTYPE html>
<html>
<head>
    <title>Code Analysis Agent</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 40px; }
        .container { max-width: 800px; }
        .endpoint { background: #f5f5f5; padding: 10px; margin: 10px 0; border-radius: 5px; }
        pre { background: #f0f0f0; padding: 15px; border-radius: 5px; overflow-x: auto; }
    </style>
</head>
<body>
    <div class="container">
        <h1>Code Analysis Agent</h1>
        <p>Intelligent code analysis agent that correlates application logs with source code using advanced git operations and lexical analysis.</p>
        
        <h2>Available Endpoints</h2>
        
        <div class="endpoint">
            <h3>GET /health</h3>
            <p>Health check endpoint</p>
        </div>
        
        <div class="endpoint">
            <h3>GET /info</h3>
            <p>Service information</p>
        </div>
        
        <div class="endpoint">
            <h3>POST /analyze</h3>
            <p>Perform code analysis</p>
            <h4>Example Request:</h4>
            <pre>{
  "cloud_account_id": "acc-123",
  "tenant": "tenant-456", 
  "workload_name": "example-app",
  "workload_namespace": "production",
  "workload_kind": "Deployment",
  "logs": "ERROR: Database connection failed at line 42",
  "prompt": "Analyze the logs for errors",
  "git_repository": {
    "url": "https://github.com/user/repo.git",
    "branch": "main"
  },
  "git_credentials": {
    "type": "token",
    "token": "your-github-token"
  }
}</pre>
        </div>
        
        <h2>CLI Usage</h2>
        <pre>./code-analysis-agent --analyze \
  --repo https://github.com/user/repo.git \
  --logs "ERROR: NullPointerException at line 42" \
  --token ghp_xxxx</pre>
    </div>
</body>
</html>
`)
	if err != nil {
		s.logger.Error(common.EventAnalysisFailure, "Failed to write root response", err, nil)
	}
}
