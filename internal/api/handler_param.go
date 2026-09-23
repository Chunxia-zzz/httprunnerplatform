package api

import (
	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

type paramHandler struct{ svc *service.ParamService }

func newParamHandler(deps *Deps) *paramHandler {
	return &paramHandler{svc: deps.Services.Param}
}

// List 数据集列表。
func (h *paramHandler) List(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	list, err := h.svc.List(id)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, list)
}

// Create 新建数据集。
func (h *paramHandler) Create(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var req service.ParamDatasetReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, response.CodeBadParam, "请求体非法："+err.Error())
		return
	}
	v, err := h.svc.Create(id, req)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, v)
}

// Get 数据集详情。
func (h *paramHandler) Get(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	v, err := h.svc.Get(id)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, v)
}

// Update 更新数据集。
func (h *paramHandler) Update(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var req service.ParamDatasetReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, response.CodeBadParam, "请求体非法："+err.Error())
		return
	}
	v, err := h.svc.Update(id, req)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, v)
}

// Delete 删除数据集。
func (h *paramHandler) Delete(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	if err := h.svc.Delete(id); err != nil {
		WriteError(c, err)
		return
	}
	OK(c, nil)
}

// CsvText 返回 CSV 文件内容（前端编辑 CSV 数据集时回填）。
func (h *paramHandler) CsvText(c *gin.Context) {
	id, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	text, err := h.svc.CsvText(id)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, gin.H{"csv_text": text})
}
