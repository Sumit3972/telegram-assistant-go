package server

import (
	"context"
	"log"
	"runtime/debug"
	"sync"

	"telegram-ai-assistant/internal/domain"
	"telegram-ai-assistant/internal/moderator"
)

type WorkerPool struct {
	workerCount int
	jobQueue    chan *domain.TelegramUpdate
	moderator   *moderator.Moderator
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.RWMutex
	isStopped   bool
}

func NewWorkerPool(workerCount int, bufferSize int, mod *moderator.Moderator) *WorkerPool {
	ctx, cancel := context.WithCancel(context.Background())
	return &WorkerPool{
		workerCount: workerCount,
		jobQueue:    make(chan *domain.TelegramUpdate, bufferSize),
		moderator:   mod,
		ctx:         ctx,
		cancel:      cancel,
	}
}

func (wp *WorkerPool) Start() {
	for i := 0; i < wp.workerCount; i++ {
		wp.wg.Add(1)
		go wp.worker(i)
	}
	log.Printf("🚀 Worker Pool started with %d workers", wp.workerCount)
}

func (wp *WorkerPool) worker(id int) {
	defer wp.wg.Done()
	for {
		select {
		case <-wp.ctx.Done():
			return
		case update, ok := <-wp.jobQueue:
			if !ok {
				return
			}
			wp.safeProcess(update)
		}
	}
}

func (wp *WorkerPool) safeProcess(update *domain.TelegramUpdate) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[WorkerPool Panic Recovery]: %v\nStack: %s", r, string(debug.Stack()))
		}
	}()

	wp.moderator.HandleUpdate(context.Background(), update)
}

func (wp *WorkerPool) Enqueue(update *domain.TelegramUpdate) bool {
	wp.mu.RLock()
	defer wp.mu.RUnlock()

	if wp.isStopped {
		return false
	}

	select {
	case <-wp.ctx.Done():
		return false
	case wp.jobQueue <- update:
		return true
	default:
		log.Printf("[WorkerPool Warning] Queue full! Spawning dynamic goroutine to process update %d", update.UpdateID)
		go wp.safeProcess(update)
		return true
	}
}

func (wp *WorkerPool) Stop() {
	wp.mu.Lock()
	if wp.isStopped {
		wp.mu.Unlock()
		return
	}
	wp.isStopped = true
	wp.cancel()
	close(wp.jobQueue)
	wp.mu.Unlock()

	wp.wg.Wait()
	log.Println("🛑 Worker Pool stopped cleanly.")
}
