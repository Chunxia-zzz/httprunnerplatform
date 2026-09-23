package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// exportHandler 把项目导出为 hrp 标准目录 zip（M4）。
type exportHandler struct{ svc serviceExporter }

// serviceExporter 只依赖导出能力本身，便于阅读这个 handler 的依赖面。
type serviceExporter interface {
	Export(projectID, envID uint64) ([]byte, string, error)
}

func newExportHandler(deps *Deps) *exportHandler {
	return &exportHandler{svc: deps.Services.Export}
}

// Download 返回项目导出 zip。
//
// 成功响应是二进制流（带 Content-Disposition 附件名），与统一 JSON 响应体不同；
// 失败时仍是统一 JSON。前端按 Content-Type 区分。
func (h *exportHandler) Download(c *gin.Context) {
	projectID, ok := ParseIDParam(c, "id")
	if !ok {
		return
	}
	envID := queryUint64(c, "env_id")

	data, filename, err := h.svc.Export(projectID, envID)
	if err != nil {
		WriteError(c, err)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
	c.Data(http.StatusOK, "application/zip", data)
}
