package announcement

// Module 聚合公告 HTTP 处理器。
type Module struct {
	Handler *Handler
}

// NewModule 创建公告 HTTP 模块。
func NewModule(handler *Handler) *Module {
	return &Module{Handler: handler}
}
