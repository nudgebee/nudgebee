package core

import (
	"sync"
	"testing"
)

// Tests swap the conversation DAO while fire-and-forget goroutines from earlier
// tests (token-usage writes) still read it, so Set must synchronize with Get.
// Under -race this fails if SetConversationDao skips the mutex.
func TestSetConversationDao_ConcurrentWithGet(t *testing.T) {
	prev := PeekConversationDao()
	t.Cleanup(func() { SetConversationDao(prev) })
	SetConversationDao(erroringConversationDao{})

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 100 {
			_ = GetConversationDao()
		}
	}()
	go func() {
		defer wg.Done()
		for range 100 {
			SetConversationDao(erroringConversationDao{})
		}
	}()
	wg.Wait()
}
