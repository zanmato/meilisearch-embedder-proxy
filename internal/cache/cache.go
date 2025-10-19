package cache

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/zanmato/meilisearch-embedder-proxy/internal/database"
	"github.com/zanmato/meilisearch-embedder-proxy/internal/hash"
	"github.com/zanmato/meilisearch-embedder-proxy/internal/openai"
	"github.com/zanmato/meilisearch-embedder-proxy/internal/tracker"
)

type Cache struct {
	db      *database.Database
	ai      *openai.Client
	hasher  *hash.Hasher
	logger  *zap.Logger
	tracker *tracker.UsageTracker
}

type EmbeddingRequest struct {
	Input interface{} `json:"input" binding:"required"` // string or []string
	Model string      `json:"model,omitempty"`
}

type EmbeddingResponse struct {
	Embedding   []float64   `json:"embedding,omitempty"`
	Embeddings  [][]float64 `json:"embeddings,omitempty"`
	Model       string      `json:"model"`
	Cached      bool        `json:"cached,omitempty"`
	CachedItems []bool      `json:"cached_items,omitempty"`
	TokenUsage  struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage,omitempty"`
}


type BatchResult struct {
	Embedding []float64
	Cached    bool
	Index     int
}

type CacheStats struct {
	TotalEntries   int64 `json:"total_entries"`
	UniqueModels   int64 `json:"unique_models"`
	AvgInputLength int64 `json:"avg_input_length"`
}

func New(db *database.Database, ai *openai.Client, hasher *hash.Hasher, tracker *tracker.UsageTracker, logger *zap.Logger) *Cache {
	return &Cache{
		db:      db,
		ai:      ai,
		hasher:  hasher,
		logger:  logger,
		tracker: tracker,
	}
}

func (c *Cache) GetEmbedding(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
	isBatch := c.isBatchInput(req.Input)

	if isBatch {
		return c.processBatchRequest(ctx, req)
	}

	return c.processSingleRequest(ctx, req)
}

func (c *Cache) isBatchInput(input interface{}) bool {
	switch input.(type) {
	case string:
		return false
	case []interface{}:
		return true
	case []string:
		return true
	default:
		return false
	}
}

func (c *Cache) normalizeInput(input interface{}) ([]string, error) {
	switch v := input.(type) {
	case string:
		return []string{v}, nil
	case []interface{}:
		result := make([]string, len(v))
		for i, item := range v {
			if str, ok := item.(string); ok {
				result[i] = str
			} else {
				return nil, fmt.Errorf("batch input item at index %d is not a string", i)
			}
		}
		return result, nil
	case []string:
		return v, nil
	default:
		return nil, fmt.Errorf("invalid input type: expected string or array of strings")
	}
}

func (c *Cache) processSingleRequest(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
	inputs, err := c.normalizeInput(req.Input)
	if err != nil {
		return nil, err
	}

	input := inputs[0]
	if input == "" {
		return nil, fmt.Errorf("input text cannot be empty")
	}

	modelName := req.Model
	if modelName == "" {
		modelName = c.ai.GetModel()
	}

	startTime := time.Now()
	inputHash := c.hasher.GenerateInputHash(input, modelName)

	c.logger.Info("Processing embedding request",
		zap.String("input_hash", inputHash[:16]+"..."),
		zap.String("model", modelName),
		zap.Int("input_length", len(input)))

	cached, err := c.db.GetCachedEmbedding(ctx, inputHash)
	if err != nil {
		c.logger.Error("Failed to check cache",
			zap.String("input_hash", inputHash[:16]+"..."),
			zap.Error(err))
		return nil, fmt.Errorf("failed to check cache: %w", err)
	}

	if cached != nil {
		c.logger.Info("Cache hit",
			zap.String("input_hash", inputHash[:16]+"..."),
			zap.Duration("lookup_time", time.Since(startTime)),
			zap.Time("cached_at", cached.CreatedAt),
			zap.Time("last_used", cached.UsedAt))

		if c.tracker != nil {
			c.tracker.TrackUsage(cached.ID)
		}

		return &EmbeddingResponse{
			Embedding: cached.EmbeddingVector,
			Model:     cached.ModelName,
			Cached:    true,
		}, nil
	}

	c.logger.Info("Cache miss, calling OpenAI API",
		zap.String("input_hash", inputHash[:16]+"..."),
		zap.Duration("lookup_time", time.Since(startTime)))

	aiResponse, err := c.ai.CreateEmbedding(ctx, input)
	if err != nil {
		c.logger.Error("Failed to create embedding via OpenAI",
			zap.String("input_hash", inputHash[:16]+"..."),
			zap.Error(err))
		return nil, fmt.Errorf("failed to create embedding: %w", err)
	}

	err = c.db.StoreEmbedding(ctx, inputHash, input, modelName, aiResponse.Embedding)
	if err != nil {
		c.logger.Error("Failed to store embedding in cache",
			zap.String("input_hash", inputHash[:16]+"..."),
			zap.Error(err))

		return &EmbeddingResponse{
			Embedding:  aiResponse.Embedding,
			Model:      aiResponse.Model,
			Cached:     false,
			TokenUsage: aiResponse.TokenUsage,
		}, nil
	}

	c.logger.Info("Successfully processed embedding request",
		zap.String("input_hash", inputHash[:16]+"..."),
		zap.String("model", modelName),
		zap.Duration("total_time", time.Since(startTime)),
		zap.Bool("cached", false),
		zap.Int("vector_length", len(aiResponse.Embedding)),
		zap.Int("prompt_tokens", aiResponse.TokenUsage.PromptTokens))

	return &EmbeddingResponse{
		Embedding:  aiResponse.Embedding,
		Model:      aiResponse.Model,
		Cached:     false,
		TokenUsage: aiResponse.TokenUsage,
	}, nil
}

func (c *Cache) GetStats(ctx context.Context) (map[string]interface{}, error) {
	stats, err := c.db.GetCacheStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cache stats: %w", err)
	}

	result := map[string]interface{}{
		"cache_stats": map[string]interface{}{
			"total_entries":    stats["total_entries"],
			"unique_models":    stats["unique_models"],
			"avg_input_length": stats["avg_input_length"],
		},
	}

	if c.tracker != nil {
		result["tracker_stats"] = c.tracker.GetStats()
	}

	return result, nil
}

func (c *Cache) processBatchRequest(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
	inputs, err := c.normalizeInput(req.Input)
	if err != nil {
		return nil, err
	}

	if len(inputs) == 0 {
		return nil, fmt.Errorf("batch input cannot be empty")
	}

	if len(inputs) > 1000 {
		return nil, fmt.Errorf("batch input too large (max 1000 items)")
	}

	modelName := req.Model
	if modelName == "" {
		modelName = c.ai.GetModel()
	}

	startTime := time.Now()

	c.logger.Info("Processing batch embedding request",
		zap.Int("batch_size", len(inputs)),
		zap.String("model", modelName))

	batchItems := c.prepareBatchItems(inputs, modelName)
	batchItems, err = c.db.GetBatchCachedEmbeddings(ctx, batchItems)
	if err != nil {
		c.logger.Error("Failed to check batch cache",
			zap.Error(err))
		return nil, fmt.Errorf("failed to check cache: %w", err)
	}

	cacheHits := 0
	cacheMisses := 0
	for _, item := range batchItems {
		if item.Cached != nil {
			cacheHits++
			if c.tracker != nil {
				c.tracker.TrackUsage(item.Cached.ID)
			}
		} else {
			cacheMisses++
		}
	}

	c.logger.Info("Batch cache check completed",
		zap.Int("cache_hits", cacheHits),
		zap.Int("cache_misses", cacheMisses),
		zap.Duration("lookup_time", time.Since(startTime)))

	uncachedItems := c.getUncachedItems(batchItems)
	var aiResponse *openai.EmbeddingResponse

	if len(uncachedItems) > 0 {
		aiResponse, err = c.createBatchEmbeddings(ctx, uncachedItems, modelName)
		if err != nil {
			c.logger.Error("Failed to create batch embeddings via OpenAI",
				zap.Error(err))
			return nil, fmt.Errorf("failed to create embeddings: %w", err)
		}

		err = c.storeBatchEmbeddings(ctx, uncachedItems, aiResponse, modelName)
		if err != nil {
			c.logger.Error("Failed to store batch embeddings in cache",
				zap.Error(err))
		}
	}

	results := c.assembleBatchResults(batchItems, uncachedItems, aiResponse, len(inputs))

	c.logger.Info("Successfully processed batch embedding request",
		zap.Int("batch_size", len(inputs)),
		zap.Int("cache_hits", cacheHits),
		zap.Int("cache_misses", cacheMisses),
		zap.Duration("total_time", time.Since(startTime)))

	return &EmbeddingResponse{
		Embeddings:  c.extractEmbeddings(results),
		Model:       modelName,
		CachedItems: c.extractCachedFlags(results),
	}, nil
}

func (c *Cache) prepareBatchItems(inputs []string, modelName string) []*database.BatchItem {
	items := make([]*database.BatchItem, len(inputs))
	for i, input := range inputs {
		items[i] = &database.BatchItem{
			Input:  input,
			Hash:   c.hasher.GenerateInputHash(input, modelName),
			Index:  i,
			Cached: nil,
		}
	}
	return items
}

func (c *Cache) getUncachedItems(batchItems []*database.BatchItem) []*database.BatchItem {
	var uncached []*database.BatchItem
	for _, item := range batchItems {
		if item.Cached == nil {
			uncached = append(uncached, item)
		}
	}
	return uncached
}

func (c *Cache) createBatchEmbeddings(ctx context.Context, uncachedItems []*database.BatchItem, modelName string) (*openai.EmbeddingResponse, error) {
	inputs := make([]string, len(uncachedItems))
	for i, item := range uncachedItems {
		inputs[i] = item.Input
	}

	return c.ai.CreateBatchEmbeddings(ctx, inputs)
}

func (c *Cache) storeBatchEmbeddings(ctx context.Context, uncachedItems []*database.BatchItem, aiResponse *openai.EmbeddingResponse, modelName string) error {
	for i, item := range uncachedItems {
		if i < len(aiResponse.Embeddings) {
			err := c.db.StoreEmbedding(ctx, item.Hash, item.Input, modelName, aiResponse.Embeddings[i])
			if err != nil {
				c.logger.Error("Failed to store batch embedding",
					zap.String("input_hash", item.Hash[:16]+"..."),
					zap.Error(err))
			}
		}
	}
	return nil
}

func (c *Cache) assembleBatchResults(batchItems []*database.BatchItem, uncachedItems []*database.BatchItem, aiResponse *openai.EmbeddingResponse, originalSize int) []*BatchResult {
	results := make([]*BatchResult, originalSize)

	for _, item := range batchItems {
		if item.Cached != nil {
			results[item.Index] = &BatchResult{
				Embedding: item.Cached.EmbeddingVector,
				Cached:    true,
				Index:     item.Index,
			}
		}
	}

	for i, item := range uncachedItems {
		if i < len(aiResponse.Embeddings) {
			results[item.Index] = &BatchResult{
				Embedding: aiResponse.Embeddings[i],
				Cached:    false,
				Index:     item.Index,
			}
		}
	}

	return results
}

func (c *Cache) extractEmbeddings(results []*BatchResult) [][]float64 {
	embeddings := make([][]float64, len(results))
	for i, result := range results {
		if result != nil {
			embeddings[i] = result.Embedding
		}
	}
	return embeddings
}

func (c *Cache) extractCachedFlags(results []*BatchResult) []bool {
	flags := make([]bool, len(results))
	for i, result := range results {
		if result != nil {
			flags[i] = result.Cached
		}
	}
	return flags
}

func (c *Cache) ValidateRequest(req *EmbeddingRequest) error {
	if req.Input == nil {
		return fmt.Errorf("input is required")
	}

	inputs, err := c.normalizeInput(req.Input)
	if err != nil {
		return err
	}

	if len(inputs) == 0 {
		return fmt.Errorf("input cannot be empty")
	}

	isBatch := c.isBatchInput(req.Input)
	if isBatch {
		if len(inputs) > 1000 {
			return fmt.Errorf("batch input too large (max 1000 items)")
		}
		for i, input := range inputs {
			if len(input) > 10000 {
				return fmt.Errorf("batch input item at index %d too long (max 10000 characters)", i)
			}
		}
	} else {
		if len(inputs[0]) > 10000 {
			return fmt.Errorf("input text too long (max 10000 characters)")
		}
	}

	if req.Model != "" && req.Model != c.ai.GetModel() {
		c.logger.Warn("Using different model than default",
			zap.String("requested_model", req.Model),
			zap.String("default_model", c.ai.GetModel()))
	}

	return nil
}

func (c *Cache) GetHashMetadata(inputText, modelName string) map[string]interface{} {
	return c.hasher.GetHashMetadata(inputText, modelName)
}

func (c *Cache) Warmup(ctx context.Context, inputs []string, modelName string) error {
	c.logger.Info("Starting cache warmup",
		zap.Int("input_count", len(inputs)),
		zap.String("model", modelName))

	for i, input := range inputs {
		select {
		case <-ctx.Done():
			c.logger.Info("Cache warmup interrupted",
				zap.Int("completed", i),
				zap.Int("total", len(inputs)))
			return ctx.Err()
		default:
		}

		req := &EmbeddingRequest{
			Input: input,
			Model: modelName,
		}

		_, err := c.GetEmbedding(ctx, req)
		if err != nil {
			c.logger.Error("Failed to warmup embedding",
				zap.Int("index", i),
				zap.String("input_preview", input[:min(50, len(input))]),
				zap.Error(err))
			continue
		}

		if (i+1)%10 == 0 {
			c.logger.Info("Cache warmup progress",
				zap.Int("completed", i+1),
				zap.Int("total", len(inputs)))
		}
	}

	c.logger.Info("Cache warmup completed",
		zap.Int("total_processed", len(inputs)))

	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
