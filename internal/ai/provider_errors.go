package ai

import (
	"io"
	"net/http"
)

// readErrorBody 读取 HTTP 错误响应体，统一处理读取失败和截断
func readErrorBody(resp *http.Response, limit int64) string {
	if resp == nil || resp.Body == nil {
		return ""
	}
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return ""
	}
	return string(bodyBytes)
}