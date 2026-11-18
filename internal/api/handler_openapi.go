package api

import (
	"io"

	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// openAPIHandler 是 OpenAPI / Swagger 导入的 HTTP 层（F5）。
type openAPIHandler struct{ svc *service.OpenAPIService }

func newOpenAPIHandler(deps *Deps) *openAPIHandler {
	return &openAPIHandler{svc: deps.Services.OpenAPI}
}

// Preview 上传文档并返回候选项（不落库）。
func (h *openAPIHandler) Preview(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	fileHeader, err := c.FormFile("file")
	if err != nil {
		Fail(c, response.CodeBadParam, "缺少上传文件字段 file")
		return
	}
	if fileHeader.Size > maxImportSize {
		Fail(c, response.CodeBadParam, "文件超过 20MB 上限")
		return
	}
	f, err := fileHeader.Open()
	if err != nil {
		Fail(c, response.CodeBadParam, "读取上传文件失败："+err.Error())
		return
	}
	defer f.Close()
	content, err := io.ReadAll(f)
	if err != nil {
		Fail(c, response.CodeBadParam, "读取上传文件失败："+err.Error())
		return
	}
	result, err := h.svc.Preview(projectID, content)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, result)
}

// Commit 按策略把勾选的接口写入接口库。
func (h *openAPIHandler) Commit(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	var req service.OpenAPICommitReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, response.CodeBadParam, "请求体非法："+err.Error())
		return
	}
	result, err := h.svc.Commit(projectID, req)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, result)
}
