package promptpreset

// Module 聚合预制提示词 HTTP 处理器。
type Module struct {
	Handler *Handler
}

// NewModule 创建预制提示词 HTTP 模块。
func NewModule(handler *Handler) *Module {
	return &Module{Handler: handler}
}
