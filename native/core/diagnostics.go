package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const diagnosticLogBytes = 10 * 1024 * 1024

type diagnosticEvent struct {
	HTTPStatus   int       `json:"httpStatus,omitempty"`
	Client       string    `json:"client,omitempty"`
	Protocol     string    `json:"protocol,omitempty"`
	CFRay        string    `json:"cfRay,omitempty"`
	ResponseType string    `json:"responseType,omitempty"`
	Time         time.Time `json:"time"`
	Level        string    `json:"level"`
	Event        string    `json:"event"`
	Source       string    `json:"source,omitempty"`
	DramaID      string    `json:"dramaId,omitempty"`
	Host         string    `json:"host,omitempty"`
	Message      string    `json:"message"`
}

type diagnosticLog struct {
	mu          sync.Mutex
	path        string
	limit       int64
	warningOnce sync.Once
}

func newDiagnosticLog(directory string) *diagnosticLog {
	log := &diagnosticLog{path: filepath.Join(directory, "logs", "app.log"), limit: diagnosticLogBytes}
	// Older versions kept a second backup.
	if err := os.Remove(log.path + ".2"); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "旧诊断日志清理失败：%v\n", PublicError(err))
	}
	return log
}

func (log *diagnosticLog) write(event diagnosticEvent) error {
	event.Time = time.Now()
	if event.Level == "" {
		event.Level = "error"
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	log.mu.Lock()
	defer log.mu.Unlock()
	if int64(len(data)) > log.limit {
		return errors.New("单条诊断日志过大")
	}
	if err := os.MkdirAll(filepath.Dir(log.path), 0700); err != nil {
		return err
	}
	info, err := os.Stat(log.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if info != nil && info.Size()+int64(len(data)) > log.limit {
		if err := os.Rename(log.path, log.path+".1"); err != nil {
			return err
		}
	}
	file, err := os.OpenFile(log.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	return errors.Join(writeErr, file.Close())
}

func (downloader *Downloader) recordDiagnostic(event diagnosticEvent) {
	if downloader == nil || downloader.diagnostics == nil {
		return
	}
	event.Message = Truncate(redactErrorString(event.Message), 4096)
	if err := downloader.diagnostics.write(event); err != nil {
		downloader.diagnostics.warningOnce.Do(func() {
			fmt.Fprintf(os.Stderr, "诊断日志保存失败：%v\n", PublicError(err))
		})
	}
}
