package database

import (
	"context"
	"database/sql"
	"fmt"
	"io/ioutil"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type Database struct {
	pool   *pgxpool.Pool
	logger *zap.Logger
}

type BatchItem struct {
	Input  string
	Hash   string
	Index  int
	Cached *CachedEmbedding
}

func (db *Database) Pool() *pgxpool.Pool {
	return db.pool
}

type CachedEmbedding struct {
	ID              uuid.UUID `json:"id"`
	InputHash       string    `json:"input_hash"`
	InputText       string    `json:"input_text"`
	EmbeddingVector []float64 `json:"embedding_vector"`
	ModelName       string    `json:"model_name"`
	InputLength     int       `json:"input_length"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	UsedAt          time.Time `json:"used_at"`
}

func New(databaseDSN string, logger *zap.Logger) (*Database, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	config, err := pgxpool.ParseConfig(databaseDSN)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database config: %w", err)
	}

	config.MaxConns = 5
	config.MinConns = 2
	config.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	db := &Database{
		pool:   pool,
		logger: logger,
	}

	if err := db.ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	logger.Info("Successfully connected to database")
	return db, nil
}

func (db *Database) ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	return db.pool.Ping(ctx)
}

func (db *Database) Close() {
	db.pool.Close()
	db.logger.Info("Database connection pool closed")
}

func (db *Database) RunMigrations(migrationsDir string) error {
	ctx := context.Background()

	files, err := ioutil.ReadDir(migrationsDir)
	if err != nil {
		return fmt.Errorf("failed to read migrations directory: %w", err)
	}

	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".sql") {
			continue
		}

		db.logger.Info("Running migration", zap.String("file", file.Name()))

		migrationPath := fmt.Sprintf("%s/%s", migrationsDir, file.Name())
		content, err := ioutil.ReadFile(migrationPath)
		if err != nil {
			return fmt.Errorf("failed to read migration file %s: %w", file.Name(), err)
		}

		if err := db.executeSQL(ctx, string(content)); err != nil {
			return fmt.Errorf("failed to execute migration %s: %w", file.Name(), err)
		}

		db.logger.Info("Migration completed", zap.String("file", file.Name()))
	}

	return nil
}

func (db *Database) executeSQL(ctx context.Context, sql string) error {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, sql); err != nil {
		return fmt.Errorf("failed to execute SQL: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

func (db *Database) GetCachedEmbedding(ctx context.Context, inputHash string) (*CachedEmbedding, error) {
	var embedding CachedEmbedding
	var embeddingVectorJSON string

	query := `
		SELECT id, input_hash, input_text, embedding_vector, model_name, input_length, created_at, updated_at, used_at
		FROM embedding_cache
		WHERE input_hash = $1
	`

	err := db.pool.QueryRow(ctx, query, inputHash).Scan(
		&embedding.ID,
		&embedding.InputHash,
		&embedding.InputText,
		&embeddingVectorJSON,
		&embedding.ModelName,
		&embedding.InputLength,
		&embedding.CreatedAt,
		&embedding.UpdatedAt,
		&embedding.UsedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query cached embedding: %w", err)
	}

	if err := db.parseEmbeddingVector(embeddingVectorJSON, &embedding.EmbeddingVector); err != nil {
		return nil, fmt.Errorf("failed to parse embedding vector: %w", err)
	}

	return &embedding, nil
}

func (db *Database) GetBatchCachedEmbeddings(ctx context.Context, batchItems []*BatchItem) ([]*BatchItem, error) {
	if len(batchItems) == 0 {
		return batchItems, nil
	}

	hashes := make([]string, len(batchItems))
	hashToItem := make(map[string]*BatchItem)

	for i, item := range batchItems {
		hashes[i] = item.Hash
		hashToItem[item.Hash] = item
	}

	query := `
		SELECT id, input_hash, input_text, embedding_vector, model_name, input_length, created_at, updated_at, used_at
		FROM embedding_cache
		WHERE input_hash = ANY($1)
	`

	rows, err := db.pool.Query(ctx, query, hashes)
	if err != nil {
		return nil, fmt.Errorf("failed to query batch cached embeddings: %w", err)
	}
	defer rows.Close()

	var embeddings []*CachedEmbedding
	for rows.Next() {
		var embedding CachedEmbedding
		var embeddingVectorJSON string

		err := rows.Scan(
			&embedding.ID,
			&embedding.InputHash,
			&embedding.InputText,
			&embeddingVectorJSON,
			&embedding.ModelName,
			&embedding.InputLength,
			&embedding.CreatedAt,
			&embedding.UpdatedAt,
			&embedding.UsedAt,
		)

		if err != nil {
			return nil, fmt.Errorf("failed to scan cached embedding: %w", err)
		}

		if err := db.parseEmbeddingVector(embeddingVectorJSON, &embedding.EmbeddingVector); err != nil {
			return nil, fmt.Errorf("failed to parse embedding vector: %w", err)
		}

		embeddings = append(embeddings, &embedding)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating batch results: %w", err)
	}

	for _, embedding := range embeddings {
		if item, exists := hashToItem[embedding.InputHash]; exists {
			item.Cached = embedding
		}
	}

	return batchItems, nil
}

func (db *Database) StoreEmbedding(ctx context.Context, inputHash, inputText, modelName string, embeddingVector []float64) error {
	embeddingJSON, err := db.serializeEmbeddingVector(embeddingVector)
	if err != nil {
		return fmt.Errorf("failed to serialize embedding vector: %w", err)
	}

	query := `
		INSERT INTO embedding_cache (input_hash, input_text, embedding_vector, model_name, input_length, used_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (input_hash) DO UPDATE SET
			embedding_vector = EXCLUDED.embedding_vector,
			updated_at = NOW(),
			used_at = NOW()
	`

	_, err = db.pool.Exec(ctx, query, inputHash, inputText, embeddingJSON, modelName, len(inputText))
	if err != nil {
		return fmt.Errorf("failed to store embedding: %w", err)
	}

	db.logger.Info("Stored embedding in cache",
		zap.String("input_hash", inputHash),
		zap.String("model", modelName),
		zap.Int("vector_length", len(embeddingVector)))

	return nil
}

func (db *Database) GetCacheStats(ctx context.Context) (map[string]int64, error) {
	query := `
		SELECT
			COUNT(*) as total_entries,
			COUNT(DISTINCT model_name) as unique_models,
			AVG(input_length) as avg_input_length
		FROM embedding_cache
	`

	var totalEntries, uniqueModels int64
	var avgInputLength float64

	err := db.pool.QueryRow(ctx, query).Scan(&totalEntries, &uniqueModels, &avgInputLength)
	if err != nil {
		return nil, fmt.Errorf("failed to get cache stats: %w", err)
	}

	stats := map[string]int64{
		"total_entries":    totalEntries,
		"unique_models":    uniqueModels,
		"avg_input_length": int64(avgInputLength),
	}

	return stats, nil
}

func (db *Database) serializeEmbeddingVector(vector []float64) (string, error) {
	return "[" + strings.Trim(strings.Replace(fmt.Sprint(vector), " ", ",", -1), "[]") + "]", nil
}

func (db *Database) parseEmbeddingVector(jsonStr string, vector *[]float64) error {
	jsonStr = strings.TrimSpace(jsonStr)
	if len(jsonStr) == 0 {
		return nil
	}

	if !strings.HasPrefix(jsonStr, "[") || !strings.HasSuffix(jsonStr, "]") {
		return fmt.Errorf("invalid JSON array format")
	}

	jsonStr = jsonStr[1 : len(jsonStr)-1]
	if len(jsonStr) == 0 {
		return nil
	}

	parts := strings.Split(jsonStr, ",")
	*vector = make([]float64, len(parts))

	for i, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		var val float64
		_, err := fmt.Sscanf(part, "%f", &val)
		if err != nil {
			return fmt.Errorf("failed to parse float value '%s': %w", part, err)
		}
		(*vector)[i] = val
	}

	return nil
}
