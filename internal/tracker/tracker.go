package tracker

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/zanmato/meilisearch-embedder-proxy/internal/database"
)

type UsageTracker struct {
	db            *database.Database
	logger        *zap.Logger
	usageChan     chan uuid.UUID
	batchSize     int
	flushInterval time.Duration
	stopChan      chan struct{}
	wg            sync.WaitGroup
	buffer        []uuid.UUID
	bufferMutex   sync.Mutex
}

func New(db *database.Database, logger *zap.Logger, batchSize int, flushInterval time.Duration) *UsageTracker {
	return &UsageTracker{
		db:            db,
		logger:        logger,
		usageChan:     make(chan uuid.UUID, 1000),
		batchSize:     batchSize,
		flushInterval: flushInterval,
		stopChan:      make(chan struct{}),
		buffer:        make([]uuid.UUID, 0, batchSize),
	}
}

func (ut *UsageTracker) Start(ctx context.Context) {
	ut.logger.Info("Starting usage tracker",
		zap.Int("batch_size", ut.batchSize),
		zap.Duration("flush_interval", ut.flushInterval))

	ut.wg.Add(2)

	go ut.processUsageUpdates(ctx)
	go ut.flushPeriodically(ctx)
}

func (ut *UsageTracker) Stop() {
	ut.logger.Info("Stopping usage tracker")

	close(ut.stopChan)
	close(ut.usageChan)

	ut.wg.Wait()

	ut.flushBuffer()

	ut.logger.Info("Usage tracker stopped")
}

func (ut *UsageTracker) TrackUsage(id uuid.UUID) {
	select {
	case ut.usageChan <- id:
	default:
		ut.logger.Warn("Usage tracking channel full, dropping usage update",
			zap.String("id", id.String()))
	}
}

func (ut *UsageTracker) processUsageUpdates(ctx context.Context) {
	defer ut.wg.Done()

	for {
		select {
		case id, ok := <-ut.usageChan:
			if !ok {
				return
			}

			ut.bufferMutex.Lock()
			ut.buffer = append(ut.buffer, id)
			shouldFlush := len(ut.buffer) >= ut.batchSize
			ut.bufferMutex.Unlock()

			if shouldFlush {
				ut.flushBuffer()
			}

		case <-ut.stopChan:
			return

		case <-ctx.Done():
			return
		}
	}
}

func (ut *UsageTracker) flushPeriodically(ctx context.Context) {
	defer ut.wg.Done()

	ticker := time.NewTicker(ut.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ut.flushBuffer()

		case <-ut.stopChan:
			return

		case <-ctx.Done():
			return
		}
	}
}

func (ut *UsageTracker) flushBuffer() {
	ut.bufferMutex.Lock()
	if len(ut.buffer) == 0 {
		ut.bufferMutex.Unlock()
		return
	}

	batch := make([]uuid.UUID, len(ut.buffer))
	copy(batch, ut.buffer)
	ut.buffer = ut.buffer[:0]
	ut.bufferMutex.Unlock()

	if err := ut.updateUsageTimestamps(batch); err != nil {
		ut.logger.Error("Failed to update usage timestamps",
			zap.Error(err),
			zap.Int("batch_size", len(batch)))
	} else {
		ut.logger.Debug("Updated usage timestamps",
			zap.Int("batch_size", len(batch)))
	}
}

func (ut *UsageTracker) updateUsageTimestamps(ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	query := `
		UPDATE embedding_cache
		SET used_at = NOW()
		WHERE id = ANY($1)
	`

	idStrings := make([]string, len(ids))
	for i, id := range ids {
		idStrings[i] = id.String()
	}

	_, err := ut.db.Pool().Exec(ctx, query, idStrings)
	return err
}

func (ut *UsageTracker) GetStats() map[string]interface{} {
	ut.bufferMutex.Lock()
	bufferLen := len(ut.buffer)
	ut.bufferMutex.Unlock()

	return map[string]interface{}{
		"buffer_size":        bufferLen,
		"channel_capacity":   cap(ut.usageChan),
		"batch_size":         ut.batchSize,
		"flush_interval_sec": ut.flushInterval.Seconds(),
	}
}
