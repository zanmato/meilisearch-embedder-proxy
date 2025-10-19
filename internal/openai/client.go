package openai

import (
	"context"
	"fmt"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"go.uber.org/zap"
)

type Client struct {
	client    *openai.Client
	logger    *zap.Logger
	model     string
	maxRetries int
	timeout   time.Duration
}

type EmbeddingRequest struct {
	Input string `json:"input"`
	Model string `json:"model,omitempty"`
}

type EmbeddingResponse struct {
	Embedding []float64  `json:"embedding,omitempty"`
	Embeddings [][]float64 `json:"embeddings,omitempty"`
	Model     string    `json:"model"`
	TokenUsage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

func New(apiKey, baseURL, model string, maxRetries int, timeoutSec int, logger *zap.Logger) (*Client, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("OpenAI API key is required")
	}

	if model == "" {
		model = "text-embedding-3-small"
	}

	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
	}

	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}

	client := openai.NewClient(opts...)

	openaiClient := &Client{
		client:     &client,
		logger:     logger,
		model:      model,
		maxRetries: maxRetries,
		timeout:    time.Duration(timeoutSec) * time.Second,
	}

	logger.Info("OpenAI client initialized",
		zap.String("model", model),
		zap.String("base_url", baseURL),
		zap.Int("max_retries", maxRetries),
		zap.Int("timeout_sec", timeoutSec))

	return openaiClient, nil
}

func (c *Client) CreateEmbedding(ctx context.Context, input string) (*EmbeddingResponse, error) {
	if input == "" {
		return nil, fmt.Errorf("input text cannot be empty")
	}

	responses, err := c.CreateBatchEmbeddings(ctx, []string{input})
	if err != nil {
		return nil, err
	}

	if len(responses.Embeddings) == 0 {
		return nil, fmt.Errorf("no embedding data returned from OpenAI")
	}

	return &EmbeddingResponse{
		Embedding: responses.Embeddings[0],
		Model:     responses.Model,
		TokenUsage: responses.TokenUsage,
	}, nil
}

func (c *Client) CreateBatchEmbeddings(ctx context.Context, inputs []string) (*EmbeddingResponse, error) {
	if len(inputs) == 0 {
		return nil, fmt.Errorf("input array cannot be empty")
	}

	if len(inputs) > 1000 {
		return nil, fmt.Errorf("batch size too large (max 1000 items)")
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	var lastErr error

	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			c.logger.Warn("Retrying OpenAI batch API call",
				zap.Int("attempt", attempt),
				zap.Error(lastErr))

			backoff := time.Duration(attempt) * time.Second
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		response, err := c.client.Embeddings.New(ctx, openai.EmbeddingNewParams{
		Input: openai.EmbeddingNewParamsInputUnion{
			OfArrayOfStrings: inputs,
		},
		Model: openai.EmbeddingModel(c.model),
	})

		if err != nil {
			lastErr = err
			c.logger.Error("OpenAI batch API call failed",
				zap.Int("attempt", attempt+1),
				zap.Error(err))
			continue
		}

		if len(response.Data) == 0 {
			lastErr = fmt.Errorf("no embedding data returned from OpenAI")
			continue
		}

		embeddings := make([][]float64, len(response.Data))
		for i, data := range response.Data {
			if len(data.Embedding) == 0 {
				lastErr = fmt.Errorf("empty embedding vector returned from OpenAI at index %d", i)
				continue
			}
			embeddings[i] = data.Embedding
		}

		if lastErr != nil {
			continue
		}

		embeddingResponse := &EmbeddingResponse{
			Embeddings: embeddings,
			Model:     string(response.Model),
		}

		if response.Usage.PromptTokens > 0 {
			embeddingResponse.TokenUsage.PromptTokens = int(response.Usage.PromptTokens)
			embeddingResponse.TokenUsage.TotalTokens = int(response.Usage.TotalTokens)
		}

		c.logger.Info("Successfully created batch embeddings",
			zap.String("model", embeddingResponse.Model),
			zap.Int("batch_size", len(embeddings)),
			zap.Int("vector_length", len(embeddings[0])),
			zap.Int("prompt_tokens", embeddingResponse.TokenUsage.PromptTokens),
			zap.Int("total_tokens", embeddingResponse.TokenUsage.TotalTokens))

		return embeddingResponse, nil
	}

	return nil, fmt.Errorf("failed to create batch embeddings after %d attempts: %w", c.maxRetries+1, lastErr)
}

func (c *Client) GetModel() string {
	return c.model
}

func (c *Client) ValidateModel(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	_, err := c.client.Models.List(ctx)

	if err != nil {
		return fmt.Errorf("model validation failed: %w", err)
	}

	c.logger.Info("Model validation successful", zap.String("model", c.model))
	return nil
}