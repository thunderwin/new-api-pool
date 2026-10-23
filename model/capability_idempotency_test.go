package model

import (
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestClaimCapabilityIdempotencyConcurrent(t *testing.T) {
	truncateTables(t)
	const goroutines = 12
	type result struct {
		claimed bool
		id      int64
		err     error
	}
	results := make(chan result, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			row, claimed, err := ClaimCapabilityIdempotency(7, "test-capability-v1", "hash-1")
			if err != nil {
				results <- result{err: err}
				return
			}
			results <- result{claimed: claimed, id: row.ID}
		}()
	}
	wg.Wait()
	close(results)

	var claimedCount int
	var expectedID int64
	for res := range results {
		require.NoError(t, res.err)
		if res.claimed {
			claimedCount++
			expectedID = res.id
		}
	}
	require.Equal(t, 1, claimedCount)

	var row CapabilityIdempotency
	require.NoError(t, DB.Where("user_id = ? AND capability_id = ? AND key_hash = ?", 7, "test-capability-v1", "hash-1").First(&row).Error)
	require.Equal(t, expectedID, row.ID)
}

func TestClaimCapabilityIdempotencyReclaimsStaleClaim(t *testing.T) {
	truncateTables(t)
	stale := CapabilityIdempotency{
		UserId:       8,
		CapabilityId: "test-capability-v1",
		KeyHash:      "hash-2",
		ClaimedAt:    common.GetTimestamp() - capabilityIdempotencyStaleSeconds - 10,
	}
	require.NoError(t, DB.Create(&stale).Error)

	row, claimed, err := ClaimCapabilityIdempotency(8, "test-capability-v1", "hash-2")
	require.NoError(t, err)
	require.True(t, claimed)
	require.GreaterOrEqual(t, row.ClaimedAt, common.GetTimestamp()-5)
}
