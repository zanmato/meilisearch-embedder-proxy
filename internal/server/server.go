package server

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/zanmato/meilisearch-embedder-proxy/internal/cache"
)

type Server struct {
	engine *gin.Engine
	logger *zap.Logger
	cache  *cache.Cache
	server *http.Server
}

type HealthResponse struct {
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
	Version   string    `json:"version"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Code    int    `json:"code"`
	Details string `json:"details,omitempty"`
}

func New(cache *cache.Cache, logger *zap.Logger) *Server {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()

	engine.Use(gin.Recovery())
	engine.Use(loggingMiddleware(logger))

	server := &Server{
		engine: engine,
		logger: logger,
		cache:  cache,
	}

	server.setupRoutes()

	return server
}

func (s *Server) setupRoutes() {
	s.engine.GET("/healthz", s.handleHealth)
	s.engine.GET("/", s.handleRoot)
	s.engine.POST("/embed", s.handleEmbed)
	s.engine.GET("/stats", s.handleStats)

	api := s.engine.Group("/api/v1")
	{
		api.POST("/embeddings", s.handleEmbed)
		api.GET("/stats", s.handleStats)
		api.GET("/healthz", s.handleHealth)
	}
}

func (s *Server) handleHealth(c *gin.Context) {
	response := HealthResponse{
		Status:    "healthy",
		Timestamp: time.Now(),
		Version:   "1.0.0",
	}

	c.JSON(http.StatusOK, response)
}

func (s *Server) handleRoot(c *gin.Context) {
	response := map[string]interface{}{
		"service": "Meep - Meilisearch Embedder Proxy",
		"version": "1.0.0",
		"endpoints": map[string]string{
			"embeddings": "POST /embed or /api/v1/embeddings",
			"stats":      "GET /stats or /api/v1/stats",
			"health":     "GET /healthz or /api/v1/healthz",
		},
		"timestamp": time.Now(),
	}

	c.JSON(http.StatusOK, response)
}

func (s *Server) handleEmbed(c *gin.Context) {
	startTime := time.Now()

	var req cache.EmbeddingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.logger.Error("Invalid request body",
			zap.Error(err),
			zap.String("client_ip", c.ClientIP()))

		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Invalid request body",
			Code:    http.StatusBadRequest,
			Details: err.Error(),
		})
		return
	}

	if err := s.cache.ValidateRequest(&req); err != nil {
		s.logger.Error("Request validation failed",
			zap.Error(err),
			zap.String("client_ip", c.ClientIP()))

		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Validation failed",
			Code:    http.StatusBadRequest,
			Details: err.Error(),
		})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()

	response, err := s.cache.GetEmbedding(ctx, &req)
	if err != nil {
		s.logger.Error("Failed to get embedding",
			zap.Error(err),
			zap.String("client_ip", c.ClientIP()),
			zap.Duration("processing_time", time.Since(startTime)))

		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "Failed to process embedding request",
			Code:    http.StatusInternalServerError,
			Details: "Internal server error",
		})
		return
	}

	s.logger.Info("Embedding request completed successfully",
		zap.String("client_ip", c.ClientIP()),
		zap.String("model", response.Model),
		zap.Bool("cached", response.Cached),
		zap.Duration("processing_time", time.Since(startTime)),
		zap.Int("vector_length", len(response.Embedding)))

	c.JSON(http.StatusOK, response)
}

func (s *Server) handleStats(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	stats, err := s.cache.GetStats(ctx)
	if err != nil {
		s.logger.Error("Failed to get cache stats",
			zap.Error(err),
			zap.String("client_ip", c.ClientIP()))

		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "Failed to retrieve statistics",
			Code:    http.StatusInternalServerError,
			Details: "Internal server error",
		})
		return
	}

	response := map[string]interface{}{
		"stats": stats,
		"service_info": map[string]interface{}{
			"service": "Meep - Meilisearch Embedder Proxy",
			"version": "1.0.0",
			"uptime":  time.Since(time.Now()).String(), // This would need to be tracked from start time
		},
		"timestamp": time.Now(),
	}

	c.JSON(http.StatusOK, response)
}

func (s *Server) Start(addr string) error {
	s.server = &http.Server{
		Addr:         addr,
		Handler:      s.engine,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	s.logger.Info("Starting HTTP server",
		zap.String("address", addr),
		zap.String("service", "Meep - Meilisearch Embedder Proxy"))

	return s.server.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("Shutting down HTTP server")

	return s.server.Shutdown(ctx)
}

func loggingMiddleware(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery

		c.Next()

		latency := time.Since(start)
		clientIP := c.ClientIP()
		method := c.Request.Method
		statusCode := c.Writer.Status()

		if raw != "" {
			path = path + "?" + raw
		}

		logger.Info("HTTP request",
			zap.String("method", method),
			zap.String("path", path),
			zap.String("client_ip", clientIP),
			zap.Int("status_code", statusCode),
			zap.Duration("latency", latency),
			zap.Int("response_size", c.Writer.Size()),
		)
	}
}
