package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func allSourcesFlavor() bool { return buildFlavor() == "zhenguojian" }

func TestNativeBuildAuthorization(t *testing.T) {
	for _, source := range []string{sourceHongguo, sourceHuangdou, sourceHuangguoVideo, sourceHuangguoAI, sourceCloudFront, sourceDSD, "unknown"} {
		allowed := source == sourceHongguo || allSourcesFlavor() && source != "unknown"
		for _, action := range []string{"catalog", "cached", "detail", "followCheck", "cover", "resolve", "enqueueDownloads", "localPlayback"} {
			input := nativeInput{Action: action, Source: source, Drama: LibraryDrama{ID: source + ":123", Source: source}}
			err := nativeAuthorizeInput(input)
			if (err == nil) != allowed {
				t.Fatalf("%s %s: allowed=%v, error=%v", action, source, allowed, err)
			}
		}
	}
	for _, input := range []nativeInput{
		{Action: "resolve", Drama: LibraryDrama{ID: "huangdou:123", Source: sourceHongguo}},
		{Action: "resolve", Drama: LibraryDrama{ID: "hongguo:123", Source: sourceHongguo}, Chapter: Chapter{Source: sourceHuangdou}},
		{Action: "resolve", Drama: LibraryDrama{ID: "hongguo:123", Source: sourceHongguo}, Chapter: Chapter{ID: "huangdou:123:1"}},
	} {
		if !errors.Is(nativeAuthorizeInput(input), errNativeBuildSource) {
			t.Fatal("mismatched source passed native authorization")
		}
	}
	manager := downloadTestManager(t)
	input := downloadTestInput(1)
	input.Entries[0].Chapter.Source = sourceHuangdou
	if _, err := manager.enqueue(input); !errors.Is(err, errNativeBuildSource) {
		t.Fatalf("mismatched download source accepted: %v", err)
	}
}

func TestNativeBuildPreservesForeignDownloadRecordsAndRestrictsScheduling(t *testing.T) {
	manager := downloadTestManager(t)
	records := []*nativeDownloadRecord{}
	for index, source := range []string{sourceHongguo, sourceHuangdou, sourceHuangguoVideo, sourceHuangguoAI, sourceCloudFront, sourceDSD} {
		drama := LibraryDrama{ID: source + ":123", Source: source, Title: "合成下载"}
		record := &nativeDownloadRecord{nativeDownloadJob: nativeDownloadJob{
			ID: nativeDownloadID(drama.ID, 1), Drama: drama, Index: 1,
			Chapter: Chapter{ID: "1", Source: source, CurrentEpisode: json.RawMessage("1")}, State: "queued", Created: int64(index),
		}}
		records = append(records, record)
	}
	data, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manager.root, "index.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	reopened := newNativeDownloads(manager.engine)
	t.Cleanup(reopened.close)
	jobs, err := reopened.snapshot()
	expected := 1
	if allSourcesFlavor() {
		expected = 6
	}
	if err != nil || len(jobs) != expected {
		t.Fatalf("wrong visible downloads: %v, %v", jobs, err)
	}
	if len(reopened.jobs) != len(records) {
		t.Fatal("changing edition discarded saved downloads")
	}
	started := make(chan string, 4)
	reopened.resolve = func(ctx context.Context, job nativeDownloadJob) (ProviderMedia, error) {
		started <- job.Drama.Source
		<-ctx.Done()
		return ProviderMedia{}, ctx.Err()
	}
	if err := reopened.control("", "resumeAll"); err != nil {
		t.Fatal(err)
	}
	workers := 1
	if allSourcesFlavor() {
		workers = 2
	}
	seen := map[string]bool{}
	for index := 0; index < workers; index++ {
		select {
		case source := <-started:
			seen[source] = true
		case <-time.After(5 * time.Second):
			t.Fatal("allowed download did not start")
		}
	}
	if !seen[sourceHongguo] || allSourcesFlavor() && !seen[sourceHuangdou] {
		t.Fatalf("wrong sources scheduled: %v", seen)
	}
	if !allSourcesFlavor() {
		reopened.mu.Lock()
		active := len(reopened.active)
		reopened.mu.Unlock()
		if active != 1 {
			t.Fatal("foreign-source task was scheduled")
		}
		for _, record := range records[1:] {
			if err := reopened.control(record.ID, "remove"); !errors.Is(err, errNativeBuildSource) {
				t.Fatalf("foreign task could be removed: %v", err)
			}
			if _, _, err := reopened.localPlan(record.Drama.ID, 1); !errors.Is(err, errNativeBuildSource) {
				t.Fatalf("foreign task could be played: %v", err)
			}
		}
	}
	if err := reopened.control("", "pauseAll"); err != nil {
		t.Fatal(err)
	}
	reopened.close()
	if !allSourcesFlavor() {
		for _, original := range records[1:] {
			if reopened.jobs[original.ID] == nil {
				t.Fatal("hidden download was removed during bulk operations")
			}
		}
		stored, err := os.ReadFile(filepath.Join(reopened.root, "index.json"))
		if err != nil {
			t.Fatal(err)
		}
		var saved []nativeDownloadRecord
		if err := json.Unmarshal(stored, &saved); err != nil || len(saved) != 6 {
			t.Fatalf("saved hidden downloads lost: %v", err)
		}
	}
	if allSourcesFlavor() {
		if job := reopened.jobs[nativeDownloadID(sourceDSD+":123", 1)]; job == nil {
			t.Fatal("DSD download was lost during bulk operations")
		}
	}
}
