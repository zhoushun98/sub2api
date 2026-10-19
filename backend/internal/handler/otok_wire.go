package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/google/wire"
)

// otok: fork 定制 handler 的 Wire 注册，在 cmd/server/wire.go 中引用。
var OtokProviderSet = wire.NewSet(
	NewOtokUSDTHandler,
	admin.NewOtokUSDTHandler,
	ProvideOtokHandlers,
)
