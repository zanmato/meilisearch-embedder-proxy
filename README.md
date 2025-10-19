# Meep - Meilisearch Embedder Proxy

A high-performance proxy service that sits between Meilisearch and OpenAI's embedding API, providing intelligent caching to reduce API calls and improve response times.

## Features

- **Intelligent Caching**: PostgreSQL-based caching of embeddings to avoid duplicate API calls
- **SHA-256 Hashing**: Content-aware deduplication using normalized text hashing
- **Batch Processing**: Efficient handling of both single and batch embedding requests
- **Partial Cache Hits**: Only uncached items in batches are sent to OpenAI API
- **Usage Tracking**: Asynchronous tracking of embedding usage with `used_at` timestamps for future cleanup
- **Configurable**: TOML-based configuration with sensible defaults
- **Performance Optimized**: Connection pooling and efficient database queries
- **Observability**: Structured logging with uber/zap
- **Graceful Shutdown**: Proper cleanup of resources
- **Health Checks**: Built-in health and statistics endpoints

## Architecture

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   Client    │────▶│   Meep      │────▶│   Cache     │────▶│  PostgreSQL │
│   Request   │     │   Proxy     │     │   Layer     │     │  Database   │
└─────────────┘     └─────────────┘     └─────────────┘     └─────────────┘
                           │
                           ▼ cache miss
                    ┌─────────────┐     ┌─────────────┐
                    │  OpenAI     │────▶│   Store     │
                    │  API        │     │   Result    │
                    └─────────────┘     └─────────────┘
```

## Quick Start

### Prerequisites

- Go 1.21 or higher
- PostgreSQL 12 or higher
- OpenAI API key

### Installation

1. Clone the repository:
```bash
git clone <repository-url>
cd meilisearch-embedder-proxy
```

2. Install dependencies:
```bash
go mod download
```

3. Set up the database:
```sql
CREATE DATABASE meep;
CREATE USER meep_user WITH PASSWORD 'your_password';
GRANT ALL PRIVILEGES ON DATABASE meep TO meep_user;
```

4. Configure the service:
```bash
cp config.toml.example config.toml
# Edit config.toml with your settings
```

5. Run the service:
```bash
go run cmd/server/main.go
```

## Configuration

The service uses a TOML configuration file. Here's the default structure:

```toml
[server]
host = "0.0.0.0"
port = 9090

[database]
host = "localhost"
port = 5432
user = "postgres"
password = ""
dbname = "meep"
sslmode = "disable"

[openai]
api_key = "your-openai-api-key"
model = "text-embedding-3-small"
base_url = "https://api.openai.com/v1"
max_retries = 3
timeout_sec = 30

[logging]
level = "info"
format = "json"
```

[tracker]
batch_size = 50          # Number of usage updates to batch together
flush_interval_sec = 5   # Seconds between automatic flushes
```

### Environment Variables

You can override configuration values using environment variables:
- `OPENAI_API_KEY`: Your OpenAI API key
- `DATABASE_PASSWORD`: PostgreSQL password
- `LOG_LEVEL`: Override logging level

## API Endpoints

### Create Embedding

**POST** `/embed` or `/api/v1/embeddings`

#### Single Input
Request body:
```json
{
  "input": "Text to embed",
  "model": "text-embedding-3-small"  // optional
}
```

Response:
```json
{
  "embedding": [0.1, 0.2, ...],
  "model": "text-embedding-3-small",
  "cached": false,
  "usage": {
    "prompt_tokens": 10,
    "total_tokens": 10
  }
}
```

#### Batch Input
Request body:
```json
{
  "input": ["Text 1", "Text 2", "Text 3"],
  "model": "text-embedding-3-small"  // optional
}
```

Response:
```json
{
  "embeddings": [
    [0.1, 0.2, ...],
    [0.3, 0.4, ...],
    [0.5, 0.6, ...]
  ],
  "model": "text-embedding-3-small",
  "cached_items": [true, false, false],
  "usage": {
    "prompt_tokens": 25,
    "total_tokens": 25
  }
}
```

## Building

### Development

```bash
go run cmd/server/main.go -config config.toml
```

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests
5. Submit a pull request
