package core

import (
	"context"
	"errors"
	"strings"
)

func (engine *nativeEngine) suggestions(ctx context.Context, source, query string) ([]string, error) {
	if strings.TrimSpace(query) == "" {
		return []string{}, nil
	}
	p, ok := engine.downloader.provider(source)
	if !ok {
		return nil, errors.New("请选择有效站源")
	}
	suggester, ok := p.(Suggester)
	if !ok {
		return []string{}, nil
	}
	return suggester.Suggestions(ctx, query)
}
