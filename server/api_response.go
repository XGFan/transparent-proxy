package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

const (
	APICodeOK             = "ok"
	APICodeInvalidRequest = "invalid_request"
	APICodeInternalError  = "internal_error"
	APICodeNotImplemented = "not_implemented"
)

type APIResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

type validatable interface {
	isValid() bool
}

func apiOK(c *gin.Context, data any) {
	apiRespond(c, http.StatusOK, APICodeOK, "ok", normalizeData(data))
}

func apiInvalidRequest(c *gin.Context, message string, details any) {
	apiRespond(c, http.StatusBadRequest, APICodeInvalidRequest, message, normalizeData(details))
}

func apiInternalError(c *gin.Context, message string, err error) {
	data := gin.H{}
	if err != nil {
		data["error"] = err.Error()
	}
	apiRespond(c, http.StatusInternalServerError, APICodeInternalError, message, data)
}

func apiNotImplemented(c *gin.Context, message string, data any) {
	apiRespond(c, http.StatusNotImplemented, APICodeNotImplemented, message, normalizeData(data))
}

func apiRespond(c *gin.Context, status int, code, message string, data any) {
	c.JSON(status, APIResponse{
		Code:    code,
		Message: message,
		Data:    normalizeData(data),
	})
}

func bindJSON(c *gin.Context, req validatable, message string) bool {
	if err := decodeJSONBodyStrict(c.Request, req); err != nil {
		apiInvalidRequest(c, message, gin.H{"error": err.Error()})
		return false
	}
	if !req.isValid() {
		apiInvalidRequest(c, message, gin.H{"error": "content is required"})
		return false
	}
	return true
}

func decodeJSONBodyStrict(req *http.Request, out any) error {
	if req == nil || req.Body == nil {
		return errors.New("request body is required")
	}
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("request body is required")
		}
		return err
	}

	var trailing any
	if err := decoder.Decode(&trailing); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return errors.New("request body must contain a single JSON object")
	}

	return errors.New("request body must contain a single JSON object")
}

func normalizeData(data any) any {
	if data == nil {
		return gin.H{}
	}
	return data
}
