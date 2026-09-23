package api

import (
	"io"

	"github.com/gin-gonic/gin"

	"github.com/Chunxia-zzz/httprunnerplatform/internal/service"
	"github.com/Chunxia-zzz/httprunnerplatform/pkg/response"
)

// importHandler 处理 HAR / Postman / curl 导入（M4）。
type importHandler struct{ svc *service.ImportService }

func newImportHandler(deps *Deps) *importHandler {
	return &importHandler{svc: deps.Services.Import}
}

const maxImportSize = 20 << 20 // 20MB：一个抓包文件的上限，再大就该让用户先裁剪

// bindImport 读 multipart 上传的 file + 可选 format 字段。
func (h *importHandler) bindImport(c *gin.Context) (service.ImportRequest, bool) {
	fileHeader, err := c.FormFile("file")
	if err != nil {
		Fail(c, response.CodeBadParam, "缺少上传文件字段 file")
		return service.ImportRequest{}, false
	}
	if fileHeader.Size > maxImportSize {
		Fail(c, response.CodeBadParam, "文件超过 20MB 上限，请先裁剪抓包内容")
		return service.ImportRequest{}, false
	}
	f, err := fileHeader.Open()
	if err != nil {
		Fail(c, response.CodeBadParam, "读取上传文件失败："+err.Error())
		return service.ImportRequest{}, false
	}
	defer f.Close()
	content, err := io.ReadAll(f)
	if err != nil {
		Fail(c, response.CodeBadParam, "读取上传文件失败："+err.Error())
		return service.ImportRequest{}, false
	}
	return service.ImportRequest{
		Format:   c.PostForm("format"),
		Filename: fileHeader.Filename,
		Content:  content,
	}, true
}

// Preview 转换并返回候选用例，不落库。
func (h *importHandler) Preview(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	req, ok := h.bindImport(c)
	if !ok {
		return
	}
	result, err := h.svc.Preview(projectID, req)
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, result)
}

// Commit 转换并落库。
func (h *importHandler) Commit(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	req, ok := h.bindImport(c)
	if !ok {
		return
	}
	result, err := h.svc.Commit(projectID, req, currentUserID(c))
	if err != nil {
		WriteError(c, err)
		return
	}
	OK(c, result)
}
