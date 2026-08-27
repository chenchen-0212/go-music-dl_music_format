package converter

import "time"

const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusSuccess   = "success"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

const (
	OutputFormatMP3 = "mp3"

	Bitrate128K = "128k"
	Bitrate192K = "192k"
	Bitrate256K = "256k"
	Bitrate320K = "320k"
)

// ConflictPolicy controls what happens when a generated MP3 already exists.
type ConflictPolicy string

const (
	ConflictRename ConflictPolicy = "rename"
	ConflictSkip   ConflictPolicy = "skip"
)

type ConvertTask struct {
	ID             string         `json:"id"`
	Input          string         `json:"input"`
	InputName      string         `json:"input_name"`
	Output         string         `json:"output"`
	Format         string         `json:"format"`
	Bitrate        string         `json:"bitrate"`
	ConflictPolicy ConflictPolicy `json:"conflict_policy"`
	Status         string         `json:"status"`
	Progress       float64        `json:"progress"`
	Error          string         `json:"error,omitempty"`
	Skipped        bool           `json:"skipped,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DurationMillis *int64         `json:"duration_millis,omitempty"`
	StartedAt      *time.Time     `json:"started_at,omitempty"`
	FinishedAt     *time.Time     `json:"finished_at,omitempty"`
	Duration       time.Duration  `json:"-"`
}

func statusText(status string) string {
	switch status {
	case StatusPending:
		return "等待中"
	case StatusRunning:
		return "转换中"
	case StatusSuccess:
		return "已完成"
	case StatusFailed:
		return "转换失败"
	case StatusCancelled:
		return "已取消"
	default:
		return status
	}
}
