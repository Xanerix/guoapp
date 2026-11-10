package core

import "context"

func (engine *nativeEngine) sourceDevices(ctx context.Context, source string, register bool) (map[string]any, error) {
	if !nativeSourceAvailable(source) {
		return map[string]any{"ready": true}, nil
	}
	p, ok := engine.downloader.provider(source)
	if !ok {
		return map[string]any{"ready": true}, nil
	}
	devices, ok := p.(DeviceProvider)
	if !ok {
		return map[string]any{"ready": true}, nil
	}
	ready, err := devices.Devices(ctx, register)
	if err != nil {
		return nil, err
	}
	if register {
		engine.mu.Lock()
		engine.commitProviderStateLocked(source)
		err := engine.writeCatalogDiskLocked()
		engine.mu.Unlock()
		if err != nil {
			return nil, err
		}
	}
	return map[string]any{"ready": ready}, nil
}

func (engine *nativeEngine) sourceAccount(ctx context.Context, source, command string, payload []byte) (any, error) {
	p, ok := engine.downloader.provider(source)
	if !ok {
		return nil, errNativeBuildSource
	}
	account, ok := p.(AccountProvider)
	if !ok {
		return nil, errNativeBuildSource
	}
	return account.Account(ctx, command, payload)
}
