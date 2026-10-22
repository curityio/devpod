package client

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"gotest.tools/assert"
)

func TestMachineStatusCacheGetsEachMachineOnce(t *testing.T) {
	cache := &MachineStatusCache{}
	calls := map[string]*atomic.Int32{"a": {}, "b": {}}

	wg := sync.WaitGroup{}
	for range 10 {
		for id, count := range calls {
			wg.Add(1)
			go func() {
				defer wg.Done()
				status, err := cache.Get(id, func() (Status, error) {
					count.Add(1)
					return StatusRunning, nil
				})
				assert.NilError(t, err)
				assert.Equal(t, status, Status(StatusRunning))
			}()
		}
	}
	wg.Wait()

	assert.Equal(t, calls["a"].Load(), int32(1))
	assert.Equal(t, calls["b"].Load(), int32(1))
}

func TestMachineStatusCacheKeepsErrors(t *testing.T) {
	cache := &MachineStatusCache{}
	calls := 0
	getStatus := func() (Status, error) {
		calls++
		return StatusNotFound, errors.New("get status: boom")
	}

	for range 2 {
		status, err := cache.Get("a", getStatus)
		assert.ErrorContains(t, err, "boom")
		assert.Equal(t, status, Status(StatusNotFound))
	}
	assert.Equal(t, calls, 1)
}

func TestNilMachineStatusCacheAlwaysGets(t *testing.T) {
	var cache *MachineStatusCache
	calls := 0
	for range 2 {
		_, _ = cache.Get("a", func() (Status, error) {
			calls++
			return StatusRunning, nil
		})
	}
	assert.Equal(t, calls, 2)
}
