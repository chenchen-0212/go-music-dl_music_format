package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/guohuiyuan/go-music-dl/internal/converter"
	"github.com/guohuiyuan/go-music-dl/internal/nativedialog"
)

var converterService *converter.Service

// RegisterConverterRoutes keeps conversion orchestration behind the module API;
// handlers only translate HTTP requests into Service calls.
func RegisterConverterRoutes(api *gin.RouterGroup, localTools bool) {
	if converterService == nil {
		converterService = converter.NewService(converter.DefaultConcurrency)
	}

	group := api.Group("/converter")
	group.POST("/tasks", createConversionTasks)
	group.GET("/tasks", listConversionTasks)
	group.GET("/tasks/:id", getConversionTask)
	group.POST("/tasks/:id/cancel", cancelConversionTask)
	group.POST("/tasks/:id/retry", retryConversionTask)
	group.DELETE("/tasks/:id", deleteConversionTask)
	group.GET("/events", streamConversionTasks)
	if localTools {
		group.GET("/picker/files", pickAudioFiles)
		group.GET("/picker/folder", pickOutputFolder)
		group.POST("/files/from-folder", audioFilesFromFolder)
	}
}

func createConversionTasks(c *gin.Context) {
	var req converter.CreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
		return
	}
	tasks, err := converterService.CreateTasks(req)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, converter.ErrTooManyFiles) {
			status = http.StatusRequestEntityTooLarge
		}
		c.JSON(status, gin.H{"error": converterErrorMessage(err)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"tasks": tasks})
}

func listConversionTasks(c *gin.Context) {
	tasks := converterService.ListTasks()
	c.JSON(http.StatusOK, tasks)
}

func getConversionTask(c *gin.Context) {
	task, err := converterService.GetTask(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "转换任务不存在"})
		return
	}
	c.JSON(http.StatusOK, task)
}

func cancelConversionTask(c *gin.Context) {
	id := c.Param("id")
	err := converterService.Cancel(id)
	writeConverterActionResponse(c, id, err)
}

func retryConversionTask(c *gin.Context) {
	id := c.Param("id")
	err := converterService.Retry(id)
	writeConverterActionResponse(c, id, err)
}

func deleteConversionTask(c *gin.Context) {
	id := c.Param("id")
	if err := converterService.Delete(id); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, converter.ErrTaskNotFound) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": converterErrorMessage(err)})
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

func writeConverterActionResponse(c *gin.Context, id string, err error) {
	if err == nil {
		task, _ := converterService.GetTask(id)
		c.JSON(http.StatusOK, task)
		return
	}
	status := http.StatusBadRequest
	if errors.Is(err, converter.ErrTaskNotFound) {
		status = http.StatusNotFound
	}
	c.JSON(status, gin.H{"error": converterErrorMessage(err)})
}

func streamConversionTasks(c *gin.Context) {
	writer := c.Writer
	header := writer.Header()
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")
	header.Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)

	events, unsubscribe := converterService.Events()
	defer unsubscribe()
	flusher, canFlush := writer.(http.Flusher)
	if canFlush {
		flusher.Flush()
	}

	clientGone := c.Request.Context().Done()
	for {
		select {
		case <-clientGone:
			return
		case event := <-events:
			payload, err := json.Marshal(event)
			if err != nil {
				continue
			}
			fmt.Fprintf(writer, "event: task\ndata: %s\n\n", payload)
			if canFlush {
				flusher.Flush()
			}
		}
	}
}

func pickAudioFiles(c *gin.Context) {
	if !isLoopbackClient(c.ClientIP()) {
		c.JSON(http.StatusForbidden, gin.H{"error": "文件选择器仅限本机使用"})
		return
	}
	files, err := nativedialog.PickFiles("选择要转换的音频")
	if writeDialogError(c, err) {
		return
	}
	if len(files) == 0 {
		c.JSON(http.StatusOK, gin.H{"files": []string{}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"files": files})
}

func pickOutputFolder(c *gin.Context) {
	if !isLoopbackClient(c.ClientIP()) {
		c.JSON(http.StatusForbidden, gin.H{"error": "目录选择器仅限本机使用"})
		return
	}
	dir, err := nativedialog.PickFolder("选择 MP3 输出目录")
	if writeDialogError(c, err) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"dir": dir})
}

func audioFilesFromFolder(c *gin.Context) {
	var req struct {
		Dir string `json:"dir"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Dir) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少目录参数"})
		return
	}
	files, err := converterService.FilesFromDirectory(req.Dir)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, converter.ErrTooManyFiles) {
			status = http.StatusRequestEntityTooLarge
		} else if errors.Is(err, converter.ErrInvalidInput) {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"error": converterErrorMessage(err)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"files": files})
}

func writeDialogError(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	status := http.StatusInternalServerError
	if errors.Is(err, nativedialog.ErrCancelled) || errors.Is(err, nativedialog.ErrUnavailable) {
		status = http.StatusConflict
	}
	c.JSON(status, gin.H{
		"error":     err.Error(),
		"cancelled": errors.Is(err, nativedialog.ErrCancelled),
	})
	return true
}

func isLoopbackClient(ip string) bool {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	return parsed != nil && parsed.IsLoopback()
}

func converterErrorMessage(err error) string {
	switch {
	case errors.Is(err, converter.ErrEmptyRequest):
		return "请先选择音频文件"
	case errors.Is(err, converter.ErrTooManyFiles):
		return "一次最多创建 1000 个任务"
	case errors.Is(err, converter.ErrInvalidInput):
		return "没有可用的音频文件，支持 FLAC、WAV、M4A、AAC、OGG、WMA"
	case errors.Is(err, converter.ErrInvalidBitrate):
		return "比特率只支持 128k、192k、256k 或 320k"
	case errors.Is(err, converter.ErrUnsupportedFormat):
		return "当前输出格式仅支持 MP3"
	case errors.Is(err, converter.ErrInvalidConflictMode):
		return "重名策略无效"
	default:
		return err.Error()
	}
}
