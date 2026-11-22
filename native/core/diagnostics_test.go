package core

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDiagnosticLogKeepsTwoFiles(t *testing.T) {
	directory := t.TempDir()
	logDir := filepath.Join(directory, "logs")
	if err := os.Mkdir(logDir, 0700); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(logDir, "app.log.2")
	if err := os.WriteFile(legacy, []byte("legacy"), 0600); err != nil {
		t.Fatal(err)
	}

	log := newDiagnosticLog(directory)
	if _, err := os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy backup still exists or could not be checked: %v", err)
	}
	event := diagnosticEvent{Event: "video.decoder", Message: "hwdec=videotoolbox"}
	if err := log.write(event); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := os.Truncate(log.path, 10*1024*1024); err != nil {
			t.Fatal(err)
		}
		if err := log.write(event); err != nil {
			t.Fatal(err)
		}
	}
	files, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Name() != "app.log" || files[1].Name() != "app.log.1" {
		t.Fatalf("unexpected diagnostic files: %v", files)
	}
	for _, file := range files {
		info, err := file.Info()
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() > 10*1024*1024 {
			t.Errorf("%s exceeds 10 MiB: %d bytes", file.Name(), info.Size())
		}
	}
}
