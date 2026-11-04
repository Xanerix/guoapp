package core

import (
	"context"
	"time"
)

func (engine *nativeEngine) hongguoDevices(ctx context.Context, register bool) (map[string]any, error) {
	if !nativeSourceAvailable(sourceHongguo) {
		return map[string]any{"ready": true}, nil
	}
	downloader := engine.downloader
	client := downloader.hongguoClient()
	if register {
		client.deviceMu.Lock()
		client.registerPausedUntil = time.Time{}
		client.deviceMu.Unlock()
		for _, helper := range []bool{false, true} {
			if err := downloader.ensureHongguoDevice(ctx, helper, ""); err != nil {
				return nil, err
			}
		}
		engine.mu.Lock()
		engine.hongguoCatalog = downloader.hongguoCatalogSnapshot()
		err := engine.writeCatalogDiskLocked()
		engine.mu.Unlock()
		if err != nil {
			return nil, err
		}
	}
	_, _, primary := client.device(false)
	_, _, helper := client.device(true)
	return map[string]any{"ready": primary && helper}, nil
}
