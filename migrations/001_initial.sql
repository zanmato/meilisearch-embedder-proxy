-- Initial migration for the meep embedding cache
-- Creates the necessary table for storing cached embeddings

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE IF NOT EXISTS embedding_cache (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    input_hash VARCHAR(64) UNIQUE NOT NULL,
    input_text TEXT NOT NULL,
    embedding_vector JSONB NOT NULL,
    model_name VARCHAR(50) NOT NULL,
    input_length INTEGER NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    used_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Create indexes for performance
CREATE INDEX IF NOT EXISTS idx_embedding_cache_hash ON embedding_cache(input_hash);
CREATE INDEX IF NOT EXISTS idx_embedding_cache_model ON embedding_cache(model_name);
CREATE INDEX IF NOT EXISTS idx_embedding_cache_created_at ON embedding_cache(created_at);
CREATE INDEX IF NOT EXISTS idx_embedding_cache_used_at ON embedding_cache(used_at);

-- Create a trigger to automatically update the updated_at timestamp
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

CREATE TRIGGER update_embedding_cache_updated_at
    BEFORE UPDATE ON embedding_cache
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Add comments for documentation
COMMENT ON TABLE embedding_cache IS 'Cache table for storing OpenAI embeddings to avoid duplicate API calls';
COMMENT ON COLUMN embedding_cache.input_hash IS 'SHA-256 hash of the input text + model name for deduplication';
COMMENT ON COLUMN embedding_cache.input_text IS 'Original input text that was embedded';
COMMENT ON COLUMN embedding_cache.embedding_vector IS 'JSON representation of the embedding vector';
COMMENT ON COLUMN embedding_cache.model_name IS 'OpenAI model used for embedding (e.g., text-embedding-3-small)';
COMMENT ON COLUMN embedding_cache.input_length IS 'Length of the original input text for analytics';
COMMENT ON COLUMN embedding_cache.used_at IS 'Last time this embedding was accessed - used for cleanup and analytics';