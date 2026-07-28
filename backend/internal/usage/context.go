package usage

import "context"

type metaContextKey struct{}

func WithMeta(ctx context.Context, patch Meta) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	base := MetaFromContext(ctx)
	merged := mergeMeta(base, patch)
	return context.WithValue(ctx, metaContextKey{}, merged)
}

func MetaFromContext(ctx context.Context) Meta {
	if ctx == nil {
		return Meta{}
	}
	meta, _ := ctx.Value(metaContextKey{}).(Meta)
	return meta
}

func mergeMeta(base, patch Meta) Meta {
	if patch.UserID > 0 {
		base.UserID = patch.UserID
	}
	if patch.SessionID > 0 {
		base.SessionID = patch.SessionID
	}
	if patch.MessageID > 0 {
		base.MessageID = patch.MessageID
	}
	if patch.RunID != "" {
		base.RunID = patch.RunID
	}
	if patch.Kind != "" {
		base.Kind = patch.Kind
	}
	if patch.Provider != "" {
		base.Provider = patch.Provider
	}
	if patch.ModelID != "" {
		base.ModelID = patch.ModelID
	}
	return base
}
