package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/require"
)

func TestCapabilityTasksExcludedFromVideoPoller(t *testing.T) {
	truncateTables(t)
	old := time.Now().Unix() - 3600
	capTask := &Task{
		TaskID:     "cap-task-1",
		Platform:   constant.TaskPlatformCapability,
		Status:     TaskStatusSubmitted,
		Progress:   "0%",
		SubmitTime: old,
	}
	videoTask := &Task{
		TaskID:     "video-task-1",
		Platform:   constant.TaskPlatformSuno,
		Status:     TaskStatusSubmitted,
		Progress:   "0%",
		SubmitTime: old,
	}
	require.NoError(t, DB.Create(capTask).Error)
	require.NoError(t, DB.Create(videoTask).Error)

	unfinished := GetAllUnFinishSyncTasks(100)
	require.Len(t, unfinished, 1)
	require.Equal(t, "video-task-1", unfinished[0].TaskID)

	timedOut := GetTimedOutUnfinishedTasks(time.Now().Unix()-60, 100)
	require.Len(t, timedOut, 1)
	require.Equal(t, "video-task-1", timedOut[0].TaskID)
}
