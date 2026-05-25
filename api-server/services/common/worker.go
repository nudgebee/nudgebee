package common

import (
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

func NewWorkerPool(name string, numWorkers int) *WorkerPool {
	if numWorkers <= 0 {
		numWorkers = 1
	}

	pool := &WorkerPool{
		tasks:      make(chan func()),
		numWorkers: numWorkers,
		name:       name,
	}
	pool.start()
	slog.Info("worker: started", "pool", name, "num_workers", numWorkers)
	return pool
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

func (p *WorkerPool) Submit(task func()) {
	if task == nil {
		slog.Warn("worker: skipping nil task", "pool", p.name)
		return
	}
	p.tasks <- task
	slog.Info("worker: pending task", "task", len(p.tasks), "pool", p.name)
}

func (p *WorkerPool) Stop() {
	slog.Info("worker: stopping, waiting for tasks to complete", "pool", p.name)
	close(p.tasks)
	p.wg.Wait()
	slog.Info("worker: stopped", "pool", p.name)
}
