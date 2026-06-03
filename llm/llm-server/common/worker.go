package common

import (
	"context"
	"log/slog"
	"runtime/debug"
	"sync"
)

type WorkerPool struct {
	tasks      chan func()
	wg         sync.WaitGroup
	numWorkers int
	name       string
}

type WorkerPoolStats struct {
	Name       string `json:"name"`
	NumWorkers int    `json:"num_workers"`
	QueueSize  int    `json:"queue_size"`
	Pending    int    `json:"pending_tasks"`
}

var (
	registeredPools []*WorkerPool
	poolMutex       sync.Mutex
)

func NewWorkerPool(name string, numWorkers int, queueSize int) *WorkerPool {
	if numWorkers <= 0 {
		numWorkers = 1
	}

	if queueSize < 0 {
		queueSize = 0
	}

	pool := &WorkerPool{
		tasks:      make(chan func(), queueSize),
		numWorkers: numWorkers,
		name:       name,
	}
	pool.start()
	RegisterWorkerPool(pool)
	slog.Info("worker: started", "pool", name, "num_workers", numWorkers)
	return pool
}

func RegisterWorkerPool(p *WorkerPool) {
	poolMutex.Lock()
	defer poolMutex.Unlock()
	registeredPools = append(registeredPools, p)
}

func UnregisterWorkerPool(p *WorkerPool) {
	poolMutex.Lock()
	defer poolMutex.Unlock()
	for i, pool := range registeredPools {
		if pool == p {
			registeredPools = append(registeredPools[:i], registeredPools[i+1:]...)
			break
		}
	}
}

func (p *WorkerPool) GetStats() WorkerPoolStats {
	return WorkerPoolStats{
		Name:       p.name,
		NumWorkers: p.numWorkers,
		QueueSize:  cap(p.tasks),
		Pending:    len(p.tasks),
	}
}

func GetAllWorkerPoolStats() []WorkerPoolStats {
	poolMutex.Lock()
	defer poolMutex.Unlock()

	stats := make([]WorkerPoolStats, 0, len(registeredPools))
	for _, p := range registeredPools {
		stats = append(stats, p.GetStats())
	}
	return stats
}

func StopAllWorkerPools() {
	poolMutex.Lock()
	// We make a copy of the slice to avoid holding the lock during potentially long Stop() calls
	pools := make([]*WorkerPool, len(registeredPools))
	copy(pools, registeredPools)
	poolMutex.Unlock()

	slog.Info("worker: stopping all registered worker pools", "count", len(pools))
	for _, p := range pools {
		p.Stop()
	}
}

func (p *WorkerPool) start() {
	for i := range p.numWorkers {
		workerID := i + 1
		p.wg.Add(1)
		go func(id int) {
			defer p.wg.Done()
			slog.Debug("worker: starting", "pool", p.name, "worker_id", id)
			for task := range p.tasks {
				p.safeExecute(task, id)
			}
		}(workerID)
	}
}

func (p *WorkerPool) safeExecute(task func(), workerID int) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("worker: task panicked",
				"pool", p.name,
				"worker_id", workerID,
				"panic", r,
				"stack", string(debug.Stack()),
			)
		}
	}()
	task()
}

func (p *WorkerPool) Submit(ctx context.Context, task func()) error {
	if task == nil {
		slog.Warn("worker: skipping nil task", "pool", p.name)
		return nil
	}

	select {
	case p.tasks <- task:
		slog.Info("worker: pending task", "task", len(p.tasks), "pool", p.name)
		return nil
	case <-ctx.Done():
		slog.Warn("worker: task submission timed out", "pool", p.name, "error", ctx.Err())
		return ErrWorkerPoolTimeout
	}
}

func (p *WorkerPool) Stop() {
	slog.Info("worker: stopping, waiting for tasks to complete", "pool", p.name)
	close(p.tasks)
	p.wg.Wait()
	UnregisterWorkerPool(p)
	slog.Info("worker: stopped", "pool", p.name)
}
