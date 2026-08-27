package converter

func cloneTask(task *ConvertTask) *ConvertTask {
	cloned := *task
	if task.DurationMillis != nil {
		duration := *task.DurationMillis
		cloned.DurationMillis = &duration
	}
	if task.StartedAt != nil {
		startedAt := *task.StartedAt
		cloned.StartedAt = &startedAt
	}
	if task.FinishedAt != nil {
		finishedAt := *task.FinishedAt
		cloned.FinishedAt = &finishedAt
	}
	return &cloned
}

func int64Ptr(value int64) *int64 {
	v := value
	return &v
}
